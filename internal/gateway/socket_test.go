package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Real clients may send the headers and the small body in separate packets.
// A rejected request must still deliver its JSON response on a close-delimited
// HTTP/1 connection when the final body packet arrives after the headers.
func TestDeniedOriginWithDelayedBodyReturnsHTTPResponse(t *testing.T) {
	a, _ := newFixture(t)
	for attempt := 0; attempt < 5; attempt++ {
		entered := make(chan struct{})
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			a.AdminHandler().ServeHTTP(w, r)
		}))
		address := s.Listener.Addr().String()
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, err = fmt.Fprintf(conn, "POST /admin/pair-code HTTP/1.1\r\nHost: %s\r\nOrigin: https://example.org\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n", address)
		if err != nil {
			conn.Close()
			s.Close()
			t.Fatal(err)
		}
		<-entered
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(conn, "{}")
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
		if err != nil {
			conn.Close()
			s.Close()
			t.Fatalf("rejection lost the HTTP response on attempt %d: %v", attempt, err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		conn.Close()
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if response.StatusCode != http.StatusForbidden || json.Unmarshal(data, &body) != nil || body.Error.Code != "admin_origin_rejected" {
			t.Fatalf("rejection response was not the expected fixed 403: status=%d", response.StatusCode)
		}
	}
}

func TestRejectedBodyDrainHasABoundedDeadline(t *testing.T) {
	a, _ := newFixture(t)
	s := httptest.NewServer(a.AdminHandler())
	defer s.Close()
	address := s.Listener.Addr().String()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	start := time.Now()
	_, err = fmt.Fprintf(conn, "POST /admin/pair-code HTTP/1.1\r\nHost: %s\r\nOrigin: https://example.org\r\nContent-Length: 2\r\nConnection: close\r\n\r\n", address)
	if err != nil {
		t.Fatal(err)
	}
	// The body deliberately never arrives. Denial must not become an
	// unbounded read of an untrusted upload.
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 || time.Since(start) > time.Second {
		t.Fatal("rejected upload drain exceeded its bound")
	}
}
