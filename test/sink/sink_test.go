package sink_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/sink"
)

func TestSinkRecordsAndWaitFor(t *testing.T) {
	s := sink.NewServer(sink.Options{Status: http.StatusCreated})
	defer s.Close()

	resp, err := http.Post(s.URL+"/hook", "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !s.WaitFor(1, time.Second) {
		t.Fatal("WaitFor timed out")
	}
	reqs := s.Requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].Path != "/hook" {
		t.Fatalf("%+v", reqs)
	}
	if string(reqs[0].Body) != `{"a":1}` {
		t.Fatalf("body %q", reqs[0].Body)
	}
	s.Reset()
	if len(s.Requests()) != 0 {
		t.Fatal("reset failed")
	}
}

func TestSinkFailThenSucceed(t *testing.T) {
	s := sink.NewServer(sink.Options{FailTimes: 2, FailStatus: http.StatusServiceUnavailable, Status: http.StatusOK})
	defer s.Close()
	for i := 0; i < 3; i++ {
		resp, err := http.Get(s.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		want := http.StatusServiceUnavailable
		if i >= 2 {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			t.Fatalf("attempt %d: got %d want %d", i, resp.StatusCode, want)
		}
	}
}

func TestSinkCloseOnFail(t *testing.T) {
	s := sink.NewServer(sink.Options{FailTimes: 1, CloseOnFail: true, Status: http.StatusOK})
	defer s.Close()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(s.URL + "/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected connection error on fail")
	}
	resp, err = client.Get(s.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestSinkDelay(t *testing.T) {
	s := sink.NewServer(sink.Options{Delay: 50 * time.Millisecond})
	defer s.Close()
	start := time.Now()
	resp, err := http.Get(s.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("delay not applied")
	}
}

func TestSinkSetOptionsAndWaitTimeout(t *testing.T) {
	s := sink.NewServer(sink.Options{})
	defer s.Close()
	s.SetOptions(sink.Options{Status: http.StatusNoContent})
	resp, err := http.Get(s.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("%d", resp.StatusCode)
	}
	if s.WaitFor(99, 30*time.Millisecond) {
		t.Fatal("expected WaitFor timeout")
	}
}
