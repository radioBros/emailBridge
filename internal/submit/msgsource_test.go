package submit

import (
	"context"
	"io"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIndexArchiveSourceNilAndBadID(t *testing.T) {
	s := &IndexArchiveSource{}
	_, err := s.Raw(context.Background(), "acc", "msg_x")
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatal(err)
	}
	s = &IndexArchiveSource{DB: nil, Objects: nopStore{}}
	_, err = s.Raw(context.Background(), "acc", store.NewID(store.PrefixMessage))
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatal(err)
	}
	s = &IndexArchiveSource{DB: &store.DB{}, Objects: nil}
	_, err = s.Raw(context.Background(), "acc", store.NewID(store.PrefixMessage))
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatal(err)
	}
}

type nopStore struct{}

func (nopStore) Put(context.Context, string, io.Reader, int64, objstore.ObjectMeta) error {
	return nil
}
func (nopStore) Get(context.Context, string) (io.ReadCloser, objstore.ObjectInfo, error) {
	return nil, objstore.ObjectInfo{}, objstore.ErrNotFound
}
