package mem

import (
	"bytes"
	"sort"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// Mailbox is an in-memory mailbox shared across sessions.
type Mailbox struct {
	tracker     *imapserver.MailboxTracker
	uidValidity uint32
	highestMod  uint64

	mutex      sync.Mutex
	name       string
	subscribed bool
	specialUse []imap.MailboxAttr
	l          []*message
	uidNext    imap.UID
}

// NewMailbox creates a mailbox with the given UIDVALIDITY.
func NewMailbox(name string, uidValidity uint32) *Mailbox {
	return &Mailbox{
		tracker:     imapserver.NewMailboxTracker(0),
		uidValidity: uidValidity,
		highestMod:  1,
		name:        name,
		uidNext:     1,
	}
}

// Name returns the mailbox name.
func (mbox *Mailbox) Name() string {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	return mbox.name
}

// UIDValidity returns the current UIDVALIDITY.
func (mbox *Mailbox) UIDValidity() uint32 {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	return mbox.uidValidity
}

// SetSpecialUse sets SPECIAL-USE attributes.
func (mbox *Mailbox) SetSpecialUse(attrs []imap.MailboxAttr) {
	mbox.mutex.Lock()
	mbox.specialUse = append([]imap.MailboxAttr(nil), attrs...)
	mbox.mutex.Unlock()
}

func (mbox *Mailbox) list(options *imap.ListOptions) *imap.ListData {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()

	if options.SelectSubscribed && !mbox.subscribed {
		return nil
	}
	if options.SelectSpecialUse && len(mbox.specialUse) == 0 {
		return nil
	}

	data := imap.ListData{Mailbox: mbox.name, Delim: mailboxDelim}
	if mbox.subscribed {
		data.Attrs = append(data.Attrs, imap.MailboxAttrSubscribed)
	}
	if (options.ReturnSpecialUse || options.SelectSpecialUse) && len(mbox.specialUse) > 0 {
		data.Attrs = append(data.Attrs, mbox.specialUse...)
	}
	if options.ReturnStatus != nil {
		data.Status = mbox.statusDataLocked(options.ReturnStatus)
	}
	return &data
}

// StatusData returns STATUS data.
func (mbox *Mailbox) StatusData(options *imap.StatusOptions) *imap.StatusData {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	return mbox.statusDataLocked(options)
}

func (mbox *Mailbox) statusDataLocked(options *imap.StatusOptions) *imap.StatusData {
	data := imap.StatusData{Mailbox: mbox.name, HighestModSeq: mbox.highestMod}
	if options.NumMessages {
		num := uint32(len(mbox.l)) //nolint:gosec // G115: mailbox size bounded by test injections
		data.NumMessages = &num
	}
	if options.UIDNext {
		data.UIDNext = mbox.uidNext
	}
	if options.UIDValidity {
		data.UIDValidity = mbox.uidValidity
	}
	if options.NumUnseen {
		num := uint32(len(mbox.l)) - mbox.countByFlagLocked(imap.FlagSeen) //nolint:gosec // G115: test store
		data.NumUnseen = &num
	}
	if options.NumDeleted {
		num := mbox.countByFlagLocked(imap.FlagDeleted)
		data.NumDeleted = &num
	}
	if options.Size {
		size := mbox.sizeLocked()
		data.Size = &size
	}
	if options.NumRecent {
		num := uint32(0)
		data.NumRecent = &num
	}
	return &data
}

func (mbox *Mailbox) countByFlagLocked(flag imap.Flag) uint32 {
	var n uint32
	for _, msg := range mbox.l {
		if _, ok := msg.flags[canonicalFlag(flag)]; ok {
			n++
		}
	}
	return n
}

func (mbox *Mailbox) sizeLocked() int64 {
	var size int64
	for _, msg := range mbox.l {
		size += int64(len(msg.buf))
	}
	return size
}

func (mbox *Mailbox) appendLiteral(r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return mbox.appendBytes(buf.Bytes(), options), nil
}

func (mbox *Mailbox) copyMsg(msg *message) *imap.AppendData {
	return mbox.appendBytes(msg.buf, &imap.AppendOptions{Time: msg.t, Flags: msg.flagList()})
}

// Inject appends a raw RFC 822 message (control API).
func (mbox *Mailbox) Inject(raw []byte, flags []imap.Flag, t time.Time) *imap.AppendData {
	return mbox.appendBytes(raw, &imap.AppendOptions{Flags: flags, Time: t})
}

func (mbox *Mailbox) appendBytes(buf []byte, options *imap.AppendOptions) *imap.AppendData {
	msg := &message{flags: make(map[imap.Flag]struct{}), buf: buf}
	if options.Time.IsZero() {
		msg.t = time.Now()
	} else {
		msg.t = options.Time
	}
	for _, flag := range options.Flags {
		msg.flags[canonicalFlag(flag)] = struct{}{}
	}

	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()

	mbox.highestMod++
	msg.mod = mbox.highestMod
	msg.uid = mbox.uidNext
	mbox.uidNext++
	mbox.l = append(mbox.l, msg)
	mbox.tracker.QueueNumMessages(uint32(len(mbox.l))) //nolint:gosec // G115: test store

	return &imap.AppendData{UIDValidity: mbox.uidValidity, UID: msg.uid}
}

func (mbox *Mailbox) rename(newName string) {
	mbox.mutex.Lock()
	mbox.name = newName
	mbox.mutex.Unlock()
}

// SetSubscribed changes the subscription state.
func (mbox *Mailbox) SetSubscribed(subscribed bool) {
	mbox.mutex.Lock()
	mbox.subscribed = subscribed
	mbox.mutex.Unlock()
}

func (mbox *Mailbox) selectDataLocked() *imap.SelectData {
	flags := mbox.flagsLocked()
	permanentFlags := make([]imap.Flag, len(flags))
	copy(permanentFlags, flags)
	permanentFlags = append(permanentFlags, imap.FlagWildcard)
	return &imap.SelectData{
		Flags:             flags,
		PermanentFlags:    permanentFlags,
		NumMessages:       uint32(len(mbox.l)), //nolint:gosec // G115: test store size
		FirstUnseenSeqNum: mbox.firstUnseenSeqNumLocked(),
		UIDNext:           mbox.uidNext,
		UIDValidity:       mbox.uidValidity,
		HighestModSeq:     mbox.highestMod,
	}
}

func (mbox *Mailbox) firstUnseenSeqNumLocked() uint32 {
	for i, msg := range mbox.l {
		if _, ok := msg.flags[canonicalFlag(imap.FlagSeen)]; !ok {
			return uint32(i) + 1
		}
	}
	return 0
}

func (mbox *Mailbox) flagsLocked() []imap.Flag {
	m := make(map[imap.Flag]struct{})
	for _, msg := range mbox.l {
		for flag := range msg.flags {
			m[flag] = struct{}{}
		}
	}
	l := make([]imap.Flag, 0, len(m))
	for flag := range m {
		l = append(l, flag)
	}
	sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
	return l
}

// SetFlags updates flags on UIDs (control API). add=true adds, false removes; set replaces when replace=true.
func (mbox *Mailbox) SetFlags(uids []imap.UID, flags []imap.Flag, op imap.StoreFlagsOp) {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	want := make(map[imap.UID]struct{}, len(uids))
	for _, u := range uids {
		want[u] = struct{}{}
	}
	for i, msg := range mbox.l {
		if _, ok := want[msg.uid]; !ok {
			continue
		}
		msg.store(&imap.StoreFlags{Op: op, Flags: flags})
		mbox.highestMod++
		msg.mod = mbox.highestMod
		mbox.tracker.QueueMessageFlags(uint32(i)+1, msg.uid, msg.flagList(), nil)
	}
}

// ExpungeDeleted removes \Deleted messages (optionally restricted to uids).
func (mbox *Mailbox) ExpungeDeleted(uids *imap.UIDSet) error {
	expunged := make(map[*message]struct{})
	mbox.mutex.Lock()
	for _, msg := range mbox.l {
		if uids != nil && !uids.Contains(msg.uid) {
			continue
		}
		if _, ok := msg.flags[canonicalFlag(imap.FlagDeleted)]; ok {
			expunged[msg] = struct{}{}
		}
	}
	mbox.mutex.Unlock()
	if len(expunged) == 0 {
		return nil
	}
	mbox.mutex.Lock()
	mbox.expungeLocked(expunged)
	mbox.mutex.Unlock()
	return nil
}

// Expunge implements the IMAP EXPUNGE path.
func (mbox *Mailbox) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	return mbox.ExpungeDeleted(uids)
}

func (mbox *Mailbox) expungeLocked(expunged map[*message]struct{}) (seqNums []uint32) {
	var filtered []*message
	for i := len(mbox.l) - 1; i >= 0; i-- {
		msg := mbox.l[i]
		if _, ok := expunged[msg]; ok {
			seqNum := uint32(i) + 1 //nolint:gosec // G115: sequence numbers from mailbox length
			seqNums = append(seqNums, seqNum)
			mbox.tracker.QueueExpunge(seqNum)
			mbox.highestMod++
		} else {
			filtered = append(filtered, msg)
		}
	}
	for i := 0; i < len(filtered)/2; i++ {
		j := len(filtered) - i - 1
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}
	mbox.l = filtered
	return seqNums
}

// Reset empties the mailbox and bumps UIDVALIDITY (control recreate).
func (mbox *Mailbox) Reset(uidValidity uint32) {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	for i := len(mbox.l); i >= 1; i-- {
		mbox.tracker.QueueExpunge(uint32(i)) //nolint:gosec // G115: sequence from length
	}
	mbox.l = nil
	mbox.uidNext = 1
	mbox.uidValidity = uidValidity
	mbox.highestMod++
}

// NewView creates a per-session view.
func (mbox *Mailbox) NewView() *MailboxView {
	return &MailboxView{Mailbox: mbox, tracker: mbox.tracker.NewSession()}
}

// MailboxView is a per-connection view with its own update queue.
type MailboxView struct {
	*Mailbox
	tracker   *imapserver.SessionTracker
	searchRes imap.UIDSet
}

// Close releases the session tracker.
func (mbox *MailboxView) Close() { mbox.tracker.Close() }

// Fetch writes FETCH responses.
func (mbox *MailboxView) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	markSeen := false
	for _, bs := range options.BodySection {
		if !bs.Peek {
			markSeen = true
			break
		}
	}
	var err error
	mbox.forEach(numSet, func(seqNum uint32, msg *message) {
		if err != nil {
			return
		}
		if options.ChangedSince > 0 && msg.mod <= options.ChangedSince {
			return
		}
		if markSeen {
			msg.flags[canonicalFlag(imap.FlagSeen)] = struct{}{}
			mbox.highestMod++
			msg.mod = mbox.highestMod
			mbox.Mailbox.tracker.QueueMessageFlags(seqNum, msg.uid, msg.flagList(), nil)
		}
		respWriter := w.CreateMessage(mbox.tracker.EncodeSeqNum(seqNum))
		err = msg.fetch(respWriter, options)
	})
	return err
}

// Search runs SEARCH/UID SEARCH.
func (mbox *MailboxView) Search(numKind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	mbox.staticSearchCriteria(criteria)
	var data imap.SearchData
	var seqSet imap.SeqSet
	var uidSet imap.UIDSet
	for i, msg := range mbox.l {
		seqNum := mbox.tracker.EncodeSeqNum(uint32(i) + 1)
		if !msg.search(seqNum, criteria) {
			continue
		}
		uidSet.AddNum(msg.uid)
		var num uint32
		switch numKind {
		case imapserver.NumKindSeq:
			if seqNum == 0 {
				continue
			}
			seqSet.AddNum(seqNum)
			num = seqNum
		case imapserver.NumKindUID:
			num = uint32(msg.uid)
		}
		if data.Min == 0 || num < data.Min {
			data.Min = num
		}
		if data.Max == 0 || num > data.Max {
			data.Max = num
		}
		data.Count++
	}
	switch numKind {
	case imapserver.NumKindSeq:
		data.All = seqSet
	case imapserver.NumKindUID:
		data.All = uidSet
	}
	if options.ReturnSave {
		mbox.searchRes = uidSet
	}
	return &data, nil
}

func (mbox *MailboxView) staticSearchCriteria(criteria *imap.SearchCriteria) {
	seqNums := make([]imap.SeqSet, 0, len(criteria.SeqNum))
	for _, seqSet := range criteria.SeqNum {
		numSet := mbox.staticNumSet(seqSet)
		switch numSet := numSet.(type) {
		case imap.SeqSet:
			seqNums = append(seqNums, numSet)
		case imap.UIDSet:
			criteria.UID = append(criteria.UID, numSet)
		}
	}
	criteria.SeqNum = seqNums
	for i, uidSet := range criteria.UID {
		criteria.UID[i] = mbox.staticNumSet(uidSet).(imap.UIDSet)
	}
	for i := range criteria.Not {
		mbox.staticSearchCriteria(&criteria.Not[i])
	}
	for i := range criteria.Or {
		for j := range criteria.Or[i] {
			mbox.staticSearchCriteria(&criteria.Or[i][j])
		}
	}
}

// Store updates flags.
func (mbox *MailboxView) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	mbox.forEach(numSet, func(seqNum uint32, msg *message) {
		msg.store(flags)
		mbox.highestMod++
		msg.mod = mbox.highestMod
		mbox.Mailbox.tracker.QueueMessageFlags(seqNum, msg.uid, msg.flagList(), mbox.tracker)
	})
	if !flags.Silent {
		return mbox.Fetch(w, numSet, &imap.FetchOptions{Flags: true})
	}
	return nil
}

// Poll drains pending updates.
func (mbox *MailboxView) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	return mbox.tracker.Poll(w, allowExpunge)
}

// Idle waits for updates until stop.
func (mbox *MailboxView) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	return mbox.tracker.Idle(w, stop)
}

func (mbox *MailboxView) forEach(numSet imap.NumSet, f func(seqNum uint32, msg *message)) {
	mbox.mutex.Lock()
	defer mbox.mutex.Unlock()
	mbox.forEachLocked(numSet, f)
}

func (mbox *MailboxView) forEachLocked(numSet imap.NumSet, f func(seqNum uint32, msg *message)) {
	numSet = mbox.staticNumSet(numSet)
	for i, msg := range mbox.l {
		seqNum := uint32(i) + 1
		var contains bool
		switch numSet := numSet.(type) {
		case imap.SeqSet:
			enc := mbox.tracker.EncodeSeqNum(seqNum)
			contains = enc != 0 && numSet.Contains(enc)
		case imap.UIDSet:
			contains = numSet.Contains(msg.uid)
		}
		if !contains {
			continue
		}
		f(seqNum, msg)
	}
}

func (mbox *MailboxView) staticNumSet(numSet imap.NumSet) imap.NumSet {
	if imap.IsSearchRes(numSet) {
		return mbox.searchRes
	}
	switch numSet := numSet.(type) {
	case imap.SeqSet:
		max := uint32(len(mbox.l)) //nolint:gosec // G115: mailbox length
		for i := range numSet {
			r := &numSet[i]
			staticNumRange(&r.Start, &r.Stop, max)
		}
	case imap.UIDSet:
		max := uint32(mbox.uidNext) - 1
		for i := range numSet {
			r := &numSet[i]
			staticNumRange((*uint32)(&r.Start), (*uint32)(&r.Stop), max)
		}
	}
	return numSet
}

func staticNumRange(start, stop *uint32, max uint32) {
	dyn := false
	if *start == 0 {
		*start = max
		dyn = true
	}
	if *stop == 0 {
		*stop = max
		dyn = true
	}
	if dyn && *start > *stop {
		*start, *stop = *stop, *start
	}
}
