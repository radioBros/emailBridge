package imapproxy

import (
	"bufio"
	"io"
	"strings"
)

type lineReader struct {
	r *bufio.Reader
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{r: bufio.NewReader(r)}
}

func (l *lineReader) readLine() (string, error) {
	s, err := l.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func (l *lineReader) buffered() []byte {
	n := l.r.Buffered()
	if n == 0 {
		return nil
	}
	b := make([]byte, n)
	_, _ = l.r.Read(b)
	return b
}
