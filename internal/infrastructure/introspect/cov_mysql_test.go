package introspect

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func covMySQL(t *testing.T) diagram.DBTarget {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mysql:8.4.3",
			ExposedPorts: []string{"3306/tcp"},
			Env:          map[string]string{"MYSQL_ROOT_PASSWORD": "rootpw", "MYSQL_DATABASE": "blog"},
			WaitingFor:   wait.ForLog("port: 3306  MySQL Community Server").WithStartupTimeout(180 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("docker/mysql unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(c) })
	host, _ := c.Host(ctx)
	mp, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(mp.Port())
	return diagram.DBTarget{Dialect: diagram.DialectMySQL, Host: host, Port: port, User: "root", Password: "rootpw", Database: "blog"}
}

func TestCovIntrospectMySQL(t *testing.T) {
	tg := covMySQL(t)
	dsn := fmt.Sprintf("root:rootpw@tcp(%s)/blog", net.JoinHostPort(tg.Host, strconv.Itoa(tg.Port)))
	var db *sql.DB
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for {
		if db, err = sql.Open("mysql", dsn); err == nil {
			if err = db.Ping(); err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("mysql not ready: %v", err)
		}
		time.Sleep(time.Second)
	}
	defer db.Close()
	for _, s := range []string{
		`create table users(id int auto_increment primary key, email varchar(100) not null unique, name varchar(50) default 'anon', score decimal(10,2) default 0, created timestamp default current_timestamp, note text null comment 'hi')`,
		`create table orders(id int primary key, uid int, a int, b int, total int generated always as (a + b) stored, kind enum('x','y') default 'x',
			constraint fk_u foreign key (uid) references users(id) on delete cascade on update set null, key idx_ab (a, b))`,
		`create table pair(x int, y int, primary key (x, y))`,
		`create table child(x int, y int, constraint fk_pair foreign key (x, y) references pair(x, y) on delete restrict on update cascade, unique key u_x (x))`,
		`create view v_users as select id, email from users`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	svc := &Service{AllowPrivate: true}
	ctx := context.Background()
	out, err := svc.Introspect(ctx, tg)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]diagram.SchemaTable{}
	for _, tb := range out.Schema.Tables {
		names[tb.Name] = tb
	}
	if len(names) != 5 || names["v_users"].Kind != "view" {
		t.Fatalf("%+v", names)
	}
	var sawFK, sawGroup, sawGen bool
	for _, c := range names["orders"].Columns {
		if c.FK != nil && c.FK.OnDelete == "CASCADE" {
			sawFK = true
		}
		if c.Name == "total" && c.Generated != "" {
			sawGen = true
		}
	}
	for _, c := range names["child"].Columns {
		if c.FK != nil && c.FK.Group != "" {
			sawGroup = true
		}
	}
	if !sawFK || !sawGroup || !sawGen {
		t.Errorf("fk %v group %v gen %v: %+v", sawFK, sawGroup, sawGen, out.Schema.Tables)
	}
	// explicit schema, ssl (server has a self-signed cert), default port
	tg2 := tg
	tg2.Schema, tg2.SSL = "blog", true
	if _, err := svc.Introspect(ctx, tg2); err != nil {
		t.Errorf("ssl: %v", err)
	}
	// wrong password: failure with the password scrubbed
	bad := tg
	bad.Password = "wrong-password"
	_, err = svc.Introspect(ctx, bad)
	if err == nil {
		t.Fatal("want auth error")
	}
	if got := err.Error(); len(got) == 0 || strings.Contains(got, "wrong-password") {
		t.Errorf("leaked: %s", got)
	}
}
