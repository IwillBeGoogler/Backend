package introspect

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func TestCovTruncate(t *testing.T) {
	if truncate("héllo", 10) != "héllo" || truncate("héllo", 2) != "hé" {
		t.Fatal("truncate")
	}
}

func covInt(i int) *int { return &i }

func TestCovPgType(t *testing.T) {
	cases := []struct {
		c    pgColumn
		want string
	}{
		{pgColumn{dataType: "character varying"}, "varchar"},
		{pgColumn{dataType: "character varying", maxLen: covInt(0)}, "varchar"},
		{pgColumn{dataType: "character varying", maxLen: covInt(20)}, "varchar(20)"},
		{pgColumn{dataType: "character"}, "char(1)"},
		{pgColumn{dataType: "character", maxLen: covInt(3)}, "char(3)"},
		{pgColumn{dataType: "numeric"}, "numeric"},
		{pgColumn{dataType: "numeric", precision: covInt(10), scale: covInt(2)}, "numeric(10,2)"},
		{pgColumn{dataType: "USER-DEFINED", udt: "mood"}, "mood"},
		{pgColumn{dataType: "ARRAY", udt: "_int4"}, "int4[]"},
		{pgColumn{dataType: "timestamp with time zone"}, "timestamptz"},
		{pgColumn{dataType: "timestamp without time zone"}, "timestamp"},
		{pgColumn{dataType: "time with time zone"}, "timetz"},
		{pgColumn{dataType: "time without time zone"}, "time"},
		{pgColumn{dataType: "integer"}, "integer"},
	}
	for _, c := range cases {
		if got := pgType(c.c); got != c.want {
			t.Errorf("%+v: %s want %s", c.c, got, c.want)
		}
	}
}

// covRows is a pgx.Rows with one row that scans to zero values.
type covRows struct {
	pgx.Rows
	n       int
	scanErr error
	err     error
}

func (r *covRows) Next() bool        { r.n++; return r.n == 1 }
func (r *covRows) Scan(...any) error { return r.scanErr }
func (r *covRows) Close()            {}
func (r *covRows) Err() error        { return r.err }

type covTx struct {
	pgx.Tx
	calls   int
	failAt  int // Query call that errors
	scanAt  int // Query call whose Scan errors
	rowsErr int // Query call whose rows.Err() errors
}

func (t *covTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	t.calls++
	if t.calls == t.failAt {
		return nil, errors.New("query boom")
	}
	r := &covRows{}
	if t.calls == t.scanAt {
		r.scanErr = errors.New("scan boom")
	}
	if t.calls == t.rowsErr {
		r.err = errors.New("rows boom")
	}
	return r, nil
}

func TestCovReadPG(t *testing.T) {
	ctx := context.Background()
	var rows pgRows
	if err := readPG(ctx, &covTx{}, "public", &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows.tables) != 1 || len(rows.enums) != 1 {
		t.Fatalf("%+v", rows)
	}
	for n := 1; n <= 6; n++ {
		for _, tx := range []*covTx{{failAt: n}, {scanAt: n}, {rowsErr: n}} {
			if err := readPG(ctx, tx, "public", &pgRows{}); err == nil {
				t.Errorf("call %d %+v: want error", n, *tx)
			}
		}
	}
}

// covPG is a scripted PostgreSQL server (trust auth, simple protocol). The
// query numbered failAt (1 = BEGIN) answers with an error; "show" queries
// return one text row.
type covPG struct {
	addr   string
	failAt int
	mu     sync.Mutex
	n      int
}

func covMsg(typ byte, body ...byte) []byte {
	b := []byte{typ, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(len(body)+4))
	return append(b, body...)
}

func covPGServer(t *testing.T, failAt int, sslRefuse bool) *covPG {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &covPG{addr: ln.Addr().String(), failAt: failAt}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c, sslRefuse)
		}
	}()
	return s
}

func (s *covPG) serve(c net.Conn, sslRefuse bool) {
	defer c.Close()
	readStartup := func() ([]byte, error) {
		var l [4]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return nil, err
		}
		b := make([]byte, binary.BigEndian.Uint32(l[:])-4)
		_, err := io.ReadFull(c, b)
		return b, err
	}
	b, err := readStartup()
	if err != nil {
		return
	}
	if binary.BigEndian.Uint32(b) == 80877103 { // SSLRequest
		c.Write([]byte("N"))
		if sslRefuse {
			return
		}
		if b, err = readStartup(); err != nil {
			return
		}
	}
	c.Write(covMsg('R', 0, 0, 0, 0))
	c.Write(covMsg('S', []byte("standard_conforming_strings\x00on\x00")...))
	c.Write(covMsg('S', []byte("client_encoding\x00UTF8\x00")...))
	c.Write(covMsg('Z', 'I'))
	for {
		var h [5]byte
		if _, err := io.ReadFull(c, h[:]); err != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(h[1:])-4)
		if _, err := io.ReadFull(c, body); err != nil {
			return
		}
		switch h[0] {
		case 'X':
			return
		case 'Q':
			q := strings.ToLower(string(body))
			s.mu.Lock()
			s.n++
			n := s.n
			s.mu.Unlock()
			if n == s.failAt {
				c.Write(covMsg('E', 'S', 'E', 'R', 'R', 'O', 'R', 0, 'C', '4', '2', '0', '0', '0', 0, 'M', 'b', 'o', 'o', 'm', 0, 0))
				c.Write(covMsg('Z', 'I'))
				continue
			}
			if strings.HasPrefix(q, "show") {
				col := append([]byte("v"), 0)
				col = append(col, 0, 0, 0, 0, 0, 0)        // table oid, attnum
				col = append(col, 0, 0, 0, 25, 0xff, 0xff) // text oid, typlen -1
				col = append(col, 0xff, 0xff, 0xff, 0xff)  // typmod
				col = append(col, 0, 0)                    // text format
				c.Write(covMsg('T', append([]byte{0, 1}, col...)...))
				c.Write(covMsg('D', 0, 1, 0, 0, 0, 3, 'o', 'f', 'f'))
				c.Write(covMsg('C', []byte("SHOW\x00")...))
			} else {
				c.Write(covMsg('C', []byte("OK\x00")...))
			}
			c.Write(covMsg('Z', 'T'))
		}
	}
}

func covTarget(s *covPG) diagram.DBTarget {
	h, p, _ := net.SplitHostPort(s.addr)
	port, _ := strconv.Atoi(p)
	return diagram.DBTarget{Dialect: diagram.DialectPostgres, Host: h, Port: port, User: "u", Password: "secret-pw", Database: "d"}
}

func TestCovWithPostgresStages(t *testing.T) {
	svc := &Service{AllowPrivate: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// 1 BEGIN, 2 SET timeout, 3 SET read only, 4 show ro, 5 show timeout
	for failAt := 1; failAt <= 5; failAt++ {
		s := covPGServer(t, failAt, false)
		err := svc.probePostgres(ctx, covTarget(s), func(string, string) {})
		if err == nil {
			t.Errorf("failAt %d: want error", failAt)
		}
	}
	s := covPGServer(t, 0, false)
	var ro, to string
	if err := svc.probePostgres(ctx, covTarget(s), func(a, b string) { ro, to = a, b }); err != nil || ro != "off" || to != "off" {
		t.Fatalf("%v %q %q", err, ro, to)
	}
	// postgres(): default schema, catalog query fails on the fake (error
	// response to the first catalog SELECT)
	s = covPGServer(t, 4, false)
	if _, err := svc.postgres(ctx, covTarget(s)); err == nil {
		t.Fatal("want catalog error")
	}
}

func TestCovWithPostgresSSLRefused(t *testing.T) {
	s := covPGServer(t, 0, true)
	tg := covTarget(s)
	tg.SSL = true
	if _, err := (&Service{AllowPrivate: true}).postgres(context.Background(), tg); err == nil {
		t.Fatal("want TLS refusal")
	}
}

func TestCovWithPostgresBadEnv(t *testing.T) {
	t.Setenv("PGSSLMODE", "bogus")
	_, err := (&Service{AllowPrivate: true}).postgres(context.Background(), diagram.DBTarget{Host: "127.0.0.1", Port: 1})
	if err == nil {
		t.Skip("pgx ignores a bad PGSSLMODE")
	}
}

func TestCovWithPostgresLookupBlocked(t *testing.T) {
	svc := &Service{Lookup: lookupTo("127.0.0.1")}
	_, err := svc.postgres(context.Background(), diagram.DBTarget{Host: "db.example.com", Port: 5432})
	if err == nil {
		t.Fatal("want blocked")
	}
}

func TestCovPostgresEmptyCatalog(t *testing.T) {
	s := covPGServer(t, 0, false)
	tg := covTarget(s)
	tg.Schema = "other"
	out, err := (&Service{AllowPrivate: true}).postgres(context.Background(), tg)
	if err != nil || len(out.Schema.Tables) != 0 {
		t.Fatalf("%v %+v", err, out)
	}
	tg.Schema = ""
	if _, err := (&Service{AllowPrivate: true}).postgres(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
}

// Introspect error mapping, driven through the scripted PostgreSQL server.
func TestCovIntrospectErrorMapping(t *testing.T) {
	ctx := context.Background()
	svc := &Service{AllowPrivate: true}

	// default port by dialect, then a blocked target
	for _, d := range []string{diagram.DialectPostgres, diagram.DialectMySQL} {
		_, err := (&Service{}).Introspect(ctx, diagram.DBTarget{Dialect: d, Host: "localhost"})
		var b *diagram.TargetBlockedError
		if !errors.As(err, &b) {
			t.Errorf("%s: %v", d, err)
		}
	}
	// unknown dialect
	_, err := svc.Introspect(ctx, diagram.DBTarget{Dialect: "oracle", Host: "127.0.0.1", Port: 1})
	var f *diagram.IntrospectFailedError
	if !errors.As(err, &f) || f.Msg != "unknown dialect" {
		t.Errorf("dialect: %v", err)
	}
	// server error: message is redacted and truncated
	s := covPGServer(t, 1, false)
	tg := covTarget(s)
	_, err = svc.Introspect(ctx, tg)
	if !errors.As(err, &f) || strings.Contains(f.Msg, "secret-pw") {
		t.Errorf("server error: %v", err)
	}
	// DNS rebinding: the check passes, the dial sees a private address
	n := 0
	rebind := &Service{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		n++
		if n == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}}
	_, err = rebind.Introspect(ctx, diagram.DBTarget{Dialect: diagram.DialectPostgres, Host: "db.example.com", Port: 5432})
	var b *diagram.TargetBlockedError
	if !errors.As(err, &b) {
		t.Errorf("rebind pg: %v", err)
	}
	n = 0
	_, err = rebind.Introspect(ctx, diagram.DBTarget{Dialect: diagram.DialectMySQL, Host: "db.example.com", Port: 3306})
	if !errors.As(err, &b) {
		t.Errorf("rebind mysql: %v", err)
	}
	// deadline: server accepts and says nothing
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	dctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_, err = svc.Introspect(dctx, diagram.DBTarget{Dialect: diagram.DialectPostgres, Host: "127.0.0.1", Port: port})
	if !errors.As(err, &f) || !strings.Contains(f.Msg, "longer than") {
		t.Errorf("deadline: %v", err)
	}
}
