package falkor

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

// mockFalkor is a minimal RESP server that echoes a fixed reply.
func mockFalkor(t *testing.T, reply string) (addr string, stop func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				// read one RESP array command
				line, _ := r.ReadString('\n')
				if !strings.HasPrefix(line, "*") {
					return
				}
				var n int
				fmt.Sscanf(line[1:], "%d", &n)
				for i := 0; i < n; i++ {
					h, _ := r.ReadString('\n') // $len
					var bl int
					fmt.Sscanf(h[1:], "%d", &bl)
					buf := make([]byte, bl+2)
					readFull(r, buf)
				}
				fmt.Fprint(c, reply)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func TestQueryParsesRows(t *testing.T) {
	// RESP: array of 2 rows, each 1 bulk string
	reply := "*2\r\n*1\r\n$8\r\nmem-0001\r\n*1\r\n$8\r\nmem-0002\r\n"
	addr, stop := mockFalkor(t, reply)
	defer stop()
	rows, err := New(addr, "pfa").RQuery(context.Background(),
		"MATCH (m:Member) RETURN m.id", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "mem-0001" || rows[1][0] != "mem-0002" {
		t.Fatalf("got %v", rows)
	}
}

func TestQueryServerError(t *testing.T) {
	addr, stop := mockFalkor(t, "-ERR unknown graph\r\n")
	defer stop()
	_, err := New(addr, "pfa").RQuery(context.Background(), "MATCH (n) RETURN n", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown graph") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestQueryDownFails(t *testing.T) {
	_, err := New("127.0.0.1:1", "pfa").RQuery(context.Background(), "x", nil)
	if err == nil {
		t.Fatal("must fail when server down")
	}
}

func TestParamSubstitutionQuotes(t *testing.T) {
	if got := quote("o'neil"); got != `'o\'neil'` {
		t.Fatalf("got %s", got)
	}
}
