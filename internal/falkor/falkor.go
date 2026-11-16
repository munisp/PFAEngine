// Package falkor integrates FalkorDB (Redis-protocol graph database) as the
// production graph store for the member-employer-agent-device fraud graph
// and the pension knowledge graph used by the KGQA assistant.
//
// FalkorDB speaks RESP with GRAPH.QUERY/GRAPH.RO_QUERY commands (Cypher).
// This client uses raw RESP over TCP (no external dependency) and is
// fail-closed: when the server is unreachable, ok=false and callers fall
// back to the in-process graph (ml/pfa_ml/graph.py nightly embeddings).
//
// Value vs Neo4j: same Cypher surface, Redis-protocol speed, much lower
// footprint — and it keeps the graph queryable at the edge while GNN
// embeddings are computed nightly in the lakehouse.
package falkor

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

type Client struct {
	addr  string
	graph string
}

func New(addr, graph string) *Client {
	if graph == "" {
		graph = "pfa"
	}
	return &Client{addr: addr, graph: graph}
}

// Query runs a read-write Cypher query; RQuery is the read-only variant.
func (c *Client) Query(ctx context.Context, cypher string, params map[string]string) ([][]string, error) {
	return c.exec(ctx, "GRAPH.QUERY", cypher, params)
}

func (c *Client) RQuery(ctx context.Context, cypher string, params map[string]string) ([][]string, error) {
	return c.exec(ctx, "GRAPH.RO_QUERY", cypher, params)
}

func (c *Client) exec(ctx context.Context, cmd, cypher string, params map[string]string) ([][]string, error) {
	for k, v := range params {
		cypher = strings.ReplaceAll(cypher, "$"+k, quote(v))
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("falkor dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// RESP array: [cmd, graph, cypher]
	args := []string{cmd, c.graph, cypher}
	fmt.Fprintf(conn, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(a), a)
	}
	return parseRESP(bufio.NewReader(conn))
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// parseRESP handles the subset FalkorDB returns: arrays, bulk strings,
// simple strings, integers, errors.
func parseRESP(r *bufio.Reader) ([][]string, error) {
	v, err := parseValue(r)
	if err != nil {
		return nil, err
	}
	return flatten(v), nil
}

func parseValue(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 {
		return nil, fmt.Errorf("falkor: short reply")
	}
	t, rest := line[0], strings.TrimRight(line[1:], "\r\n")
	switch t {
	case '+':
		return rest, nil
	case '-':
		return nil, fmt.Errorf("falkor error: %s", rest)
	case ':':
		return rest, nil
	case '$':
		var n int
		fmt.Sscanf(rest, "%d", &n)
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := readFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		var n int
		fmt.Sscanf(rest, "%d", &n)
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := parseValue(r)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	return nil, fmt.Errorf("falkor: unknown RESP type %q", t)
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func flatten(v any) [][]string {
	rows := [][]string{}
	arr, ok := v.([]any)
	if !ok {
		return rows
	}
	for _, item := range arr {
		if row, ok := item.([]any); ok {
			cells := make([]string, 0, len(row))
			for _, c := range row {
				if s, ok := c.(string); ok {
					cells = append(cells, s)
				}
			}
			rows = append(rows, cells)
		}
	}
	return rows
}

// UpsertMemberGraph mirrors a member's edges into the graph store.
func (c *Client) UpsertMemberGraph(ctx context.Context, memberID, employerID, agentID string) error {
	_, err := c.Query(ctx, `
MERGE (m:Member {id: $m})
MERGE (e:Employer {id: $e})
MERGE (a:Agent {id: $a})
MERGE (m)-[:EMPLOYED_BY]->(e)
MERGE (m)-[:ONBOARDED_BY]->(a)`,
		map[string]string{"m": memberID, "e": employerID, "a": agentID})
	return err
}

// FraudRingMembers returns members sharing a device with the given member —
// the classic bust-out/ATO ring lookup used by investigators.
func (c *Client) FraudRingMembers(ctx context.Context, memberID string) ([]string, error) {
	rows, err := c.RQuery(ctx, `
MATCH (m:Member {id: $m})-[:USED]->(d:Device)<-[:USED]-(o:Member)
RETURN DISTINCT o.id`, map[string]string{"m": memberID})
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, row := range rows {
		if len(row) > 0 && row[0] != memberID {
			out = append(out, row[0])
		}
	}
	return out, nil
}
