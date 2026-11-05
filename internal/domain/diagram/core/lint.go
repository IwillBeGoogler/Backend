package core

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Rule-based lint (core/lint.ts): architecture, ERD and UML checks over a
// page. Pure and deterministic.

// RuleDef describes a lint rule.
type RuleDef struct {
	ID, Category, Title, Description, Severity string
}

// LintRules are the rules, in report order.
var LintRules = []RuleDef{
	{"arch/spof", "architecture", "Single point of failure", "A load balancer that sends to only one instance, so there is no redundancy behind it.", "warning"},
	{"arch/db-no-replica", "architecture", "Database without a replica", "A database with no replica, standby or replicated cluster.", "warning"},
	{"arch/db-no-backup", "architecture", "Database without a backup", "A database with no connection to backup storage.", "info"},
	{"arch/public-no-waf", "architecture", "Public service without WAF or load balancer", "A client or the internet talks straight to a service.", "warning"},
	{"arch/sync-cycle", "architecture", "Cycle of synchronous calls", "Services that call each other in a loop can deadlock or amplify load.", "warning"},
	{"arch/no-monitoring", "architecture", "No monitoring connection", "A service or database that no monitoring component is connected to.", "info"},
	{"arch/cluster-profile", "architecture", "Cluster profile warning", "Problems found by the cluster profile (replication factor, quorum, zone spread).", "warning"},
	{"erd/fk-index", "erd", "Foreign key without an index", "Joins and deletes on the parent scan the child table.", "warning"},
	{"erd/no-pk", "erd", "Table without a primary key", "Rows cannot be identified.", "error"},
	{"erd/fk-type", "erd", "Foreign key type differs from the referenced key", "The column and the key it references have different types.", "error"},
	{"erd/nullable-fk", "erd", "Nullable foreign key on a required relation", "The relation says the parent is required but the column allows NULL.", "warning"},
	{"erd/naming", "erd", "Naming convention", "Table and column names should follow one style (Settings > Lint).", "info"},
	{"uml/inheritance-cycle", "uml", "Inheritance cycle", "A class is its own ancestor.", "error"},
	{"uml/interface-no-impl", "uml", "Interface without implementation", "No class realizes this interface.", "info"},
	{"uml/unused-class", "uml", "Unused class", "A class with no relations to anything and no sequence diagram participant of the same name.", "info"},
	{"uml/seq-missing-method", "uml", "Sequence message calls a missing method", "A sequence message calls a method that the class of the same name as the target participant does not have (own or inherited).", "warning"},
	{"uml/object-unknown-slot", "uml", "Object slot not in its class", "A slot of an object that names no attribute of its class (own or inherited).", "warning"},
}

// RuleCfg re-levels or disables one rule.
type RuleCfg struct {
	Enabled  *bool
	Severity string
}

// LintConfig configures LintPage.
type LintConfig struct {
	Rules  map[string]RuleCfg
	Naming string // snake_case (default), camelCase or any
}

// Problem is one lint finding.
type Problem struct {
	ID       string
	Rule     string
	Severity string
	Message  string
	NodeIDs  []string
	EdgeIDs  []string
	Fix      string // the quick fix's label
}

type roleRe struct {
	re   *regexp.Regexp
	role string
}

var idRoles = []roleRe{
	{regexp.MustCompile(`^(user|users|browser|mobile|desktop|iot|admin|internet)$`), "client"},
	{regexp.MustCompile(`(^|-)(waf|cloud-armor|firewall)(-|$)|^aws-waf$|^cloudflare$`), "waf"},
	{regexp.MustCompile(`(^|-)(load-balanc\w*|haproxy|traefik|envoy|reverse-proxy|nginx|lb|alb|nlb|elb)(-|$)|^aws-elastic-load-balancing$`), "lb"},
	{regexp.MustCompile(`api-gateway|^kong$|^aws-api-gateway$`), "gateway"},
	{regexp.MustCompile(`^(cdn|aws-amazon-cloudfront|gcp-cloud-cdn|akamai|fastly)$`), "cdn"},
	{regexp.MustCompile(`^(redis|valkey|keydb|dragonfly|memcached|cache|hazelcast|aerospike|aws-amazon-elasticache|gcp-memorystore|azure-cache-for-redis)$`), "cache"},
	{regexp.MustCompile(`^(queue|pubsub|event-bus|kafka|rabbitmq|nats|activemq|pulsar|redis-streams|sqs|sns|kinesis|gcp-pubsub|service-bus|event-hubs|eventbridge|redpanda|stream-processor)$`), "queue"},
	{regexp.MustCompile(`^(object-storage|file-storage|s3|minio|gcs|azure-blob|ceph|hdfs|nfs|backblaze|cloudflare-r2|aws-amazon-s3-glacier)$`), "storage"},
	{regexp.MustCompile(`^(prometheus|grafana|loki|tempo|mimir|thanos|alertmanager|jaeger|zipkin|opentelemetry|datadog|newrelic|sentry|pagerduty|cloudwatch|azure-monitor|gcp-monitoring|victoriametrics|elasticsearch-monitoring|kibana|logstash|fluentd|fluent-bit|vector|telegraf|netdata|zabbix|nagios|aws-amazon-managed-grafana|aws-amazon-managed-service-for-prometheus)$`), "monitoring"},
	{regexp.MustCompile(`^(database|nosql|postgresql|mysql|mariadb|oracle-db|sqlserver|sqlite|mongodb|cassandra|cockroachdb|yugabytedb|couchbase|couchdb|dynamodb|aurora|cloud-sql|spanner|cosmosdb|firestore|documentdb|tidb|neon|supabase|planetscale|vitess|clickhouse|timescaledb|influxdb|data-warehouse|aws-amazon-rds|azure-sql\w*|azure-database-\w+)$`), "database"},
}

var (
	reDbLabel      = regexp.MustCompile(`\b(database|db|postgres|mysql)\b`)
	reReplicaLabel = regexp.MustCompile(`(?i)replica|standby|secondary|follower|read[- ]only`)
	reBackupLabel  = regexp.MustCompile(`(?i)backup|snapshot|archive|dump`)
	reAsyncLabel   = regexp.MustCompile(`(?i)async|event|publish|consume|subscribe|stream|batch|cron|queue|notify|webhook`)
	asyncPresets   = map[string]bool{"metrics": true, "logs": true, "traces": true, "backup": true}
	reSnake        = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	reCamel        = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	reParens       = regexp.MustCompile(`\(.*\)`)
	reSpaces       = regexp.MustCompile(`\s+`)
	reSeqNum       = regexp.MustCompile(`^\d+[.):]\s*`)
	reSeqNew       = regexp.MustCompile(`^(new|await)\s+`)
	reSeqCall      = regexp.MustCompile(`^([A-Za-z_$][\w$]*)\s*(\(.*\))?\s*$`)
	reSeqLower     = regexp.MustCompile(`^[a-z_$]`)
)

// RoleOf classifies an architecture node.
func RoleOf(n Obj) string {
	// only architecture nodes have a role; UML / ER / board nodes never get architecture advice
	switch str(n["type"]) {
	case "icon", "cluster", "card":
	default:
		return "other"
	}
	d := dataOf(n)
	id := ""
	if d["iconId"] != nil {
		id = jsStr(d["iconId"])
	}
	label := ""
	if d["label"] != nil {
		label = strings.ToLower(jsStr(d["label"]))
	}
	profile := ""
	if d["profileId"] != nil {
		profile = jsStr(d["profileId"])
	}
	for _, r := range idRoles {
		if r.re.MatchString(id) || (profile != "" && r.re.MatchString(profile)) {
			return r.role
		}
	}
	var tags []string
	if id != "" {
		if h, ok := ResolveIcon(id); ok {
			tags = h.Tags
		}
	}
	if contains(tags, "sql") || contains(tags, "nosql") || contains(tags, "data-warehouse") {
		return "database"
	}
	if contains(tags, "queue") || contains(tags, "streaming") {
		return "queue"
	}
	if contains(tags, "observability") {
		return "monitoring"
	}
	if reDbLabel.MatchString(label) && id == "" {
		return "database"
	}
	switch str(n["type"]) {
	case "icon", "cluster", "card":
		return "service"
	}
	return "other"
}

func labelStr(v any) string {
	if v == nil {
		return ""
	}
	return jsStr(v)
}

func jsNumber(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return 0
		}
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nan()
		}
		return f
	case nil:
		return 0
	}
	return nan()
}

func nan() float64 { var z float64; return z / z }

func isReplicaNode(n Obj) bool {
	d := dataOf(n)
	return d["role"] == "replica" || d["role"] == "standby" || reReplicaLabel.MatchString(labelStr(d["label"]))
}

func redundant(n Obj) bool {
	d := dataOf(n)
	r := d["replicas"]
	if r == nil {
		r = 1.0
	}
	return jsNumber(r) > 1 || truthy(d["repeatOf"]) || (str(n["type"]) == "cluster" && d["topology"] != "single")
}

func sccs(ids []string, adj map[string][]string) [][]string {
	idx := 0
	index := map[string]int{}
	low := map[string]int{}
	on := map[string]bool{}
	var stack []string
	var out [][]string
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = idx, idx
		idx++
		stack = append(stack, v)
		on[v] = true
		for _, w := range adj[v] {
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			out = append(out, comp)
		}
	}
	for _, v := range ids {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}
	return out
}

var typeAlias = map[string]string{"int": "int", "integer": "int", "int4": "int", "serial": "int", "serial4": "int", "smallint": "smallint", "int2": "smallint", "smallserial": "smallint", "bigint": "bigint", "int8": "bigint", "bigserial": "bigint", "serial8": "bigint", "varchar": "text", "character varying": "text", "text": "text", "char": "text", "uuid": "uuid", "bool": "boolean", "boolean": "boolean", "timestamptz": "timestamp", "timestamp with time zone": "timestamp", "timestamp without time zone": "timestamp", "datetime": "timestamp", "float8": "double", "double precision": "double", "float4": "float", "real": "float", "decimal": "numeric"}

func baseType(t any) string {
	x := strings.TrimSpace(reSpaces.ReplaceAllString(reParens.ReplaceAllString(strings.ToLower(labelStr(t)), ""), " "))
	if m, ok := typeAlias[x]; ok {
		return m
	}
	return x
}

func inList(v string, l ...string) bool { return contains(l, v) }

func ids(ns []Obj) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = str(n["id"])
	}
	return out
}

// LintPage runs the rules over a page (normalized JSON).
func LintPage(page Obj, cfg LintConfig) []Problem {
	var out []Problem
	nodes := nodesOf(page)
	byID := map[string]Obj{}
	for _, n := range nodes {
		byID[str(n["id"])] = n
	}
	label := func(id string) string {
		if n := byID[id]; n != nil {
			if l := dataOf(n)["label"]; l != nil {
				return jsStr(l)
			}
		}
		return id
	}
	sevOf := func(id string) string {
		c := cfg.Rules[id]
		if c.Enabled != nil && !*c.Enabled {
			return ""
		}
		if c.Severity != "" {
			return c.Severity
		}
		for _, r := range LintRules {
			if r.ID == id {
				return r.Severity
			}
		}
		return ""
	}
	push := func(rule, key, msg string, nodeIDs []string, edgeIDs []string, fix string) {
		sev := sevOf(rule)
		if sev == "" {
			return
		}
		if nodeIDs == nil {
			nodeIDs = []string{}
		}
		out = append(out, Problem{ID: rule + ":" + key, Rule: rule, Severity: sev, Message: msg, NodeIDs: nodeIDs, EdgeIDs: edgeIDs, Fix: fix})
	}
	var edges []Obj
	for _, e := range edgesOf(page) {
		if byID[str(e["source"])] != nil && byID[str(e["target"])] != nil {
			edges = append(edges, e)
		}
	}
	outs := func(id string) []Obj {
		var r []Obj
		for _, e := range edges {
			if str(e["source"]) == id {
				r = append(r, e)
			}
		}
		return r
	}
	ins := func(id string) []Obj {
		var r []Obj
		for _, e := range edges {
			if str(e["target"]) == id {
				r = append(r, e)
			}
		}
		return r
	}
	role := map[string]string{}
	for _, n := range nodes {
		role[str(n["id"])] = RoleOf(n)
	}
	var dbs []Obj
	for _, n := range nodes {
		if role[str(n["id"])] == "database" {
			dbs = append(dbs, n)
		}
	}

	// ---- architecture ----
	for _, lb := range nodes {
		if role[str(lb["id"])] != "lb" {
			continue
		}
		lid := str(lb["id"])
		var targets []Obj
		seen := map[string]bool{}
		for _, e := range outs(lid) {
			t := str(e["target"])
			if seen[t] {
				continue
			}
			seen[t] = true
			tn := byID[t]
			if inList(role[t], "service", "cache", "database", "other") && str(tn["type"]) != "group" {
				targets = append(targets, tn)
			}
		}
		if len(targets) == 1 && !redundant(targets[0]) {
			tid := str(targets[0]["id"])
			push("arch/spof", lid, label(lid)+" sends traffic to only one instance ("+label(tid)+"): there is no redundancy behind it. Add a second instance or set replicas.", []string{lid, tid}, ids(outs(lid)), "")
		}
	}
	for _, db := range dbs {
		did := str(db["id"])
		type peer struct{ e, other Obj }
		var peers []peer
		for _, e := range append(append([]Obj{}, ins(did)...), outs(did)...) {
			o := str(e["source"])
			if o == did {
				o = str(e["target"])
			}
			peers = append(peers, peer{e, byID[o]})
		}
		dd := dataOf(db)
		hasReplica := redundant(db) || truthy(dd["replication"]) || isReplicaNode(db)
		if !hasReplica {
			for _, p := range peers {
				if role[str(p.other["id"])] == "database" && (obj(p.e["data"])["kind"] == "replication" || isReplicaNode(p.other) || dataOf(p.other)["iconId"] == dd["iconId"]) {
					hasReplica = true
					break
				}
			}
		}
		if !hasReplica && !isReplicaNode(db) {
			push("arch/db-no-replica", did, label(did)+" has no replica or standby: a failure loses availability. Add a replica or use a replicated cluster.", []string{did}, nil, "")
		}
		hasBackup := false
		for _, p := range peers {
			if role[str(p.other["id"])] == "storage" || reBackupLabel.MatchString(labelStr(p.e["label"])) || obj(p.e["data"])["preset"] == "backup" || reBackupLabel.MatchString(labelStr(dataOf(p.other)["label"])) {
				hasBackup = true
				break
			}
		}
		if !hasBackup && !isReplicaNode(db) {
			push("arch/db-no-backup", did, label(did)+` has no backup: connect it to backup storage (or label the edge "backup").`, []string{did}, nil, "")
		}
	}
	shields := []string{"lb", "gateway", "waf", "cdn", "client", "monitoring", "queue"}
	for _, e := range edges {
		from, to := byID[str(e["source"])], byID[str(e["target"])]
		fid, tid := str(from["id"]), str(to["id"])
		if role[fid] != "client" {
			continue
		}
		tr := role[tid]
		if inList(tr, shields...) || tr == "other" || tr == "storage" {
			continue
		}
		push("arch/public-no-waf", fid+">"+tid, label(fid)+" talks directly to "+label(tid)+". Put a WAF or a load balancer in front of it.", []string{fid, tid}, []string{str(e["id"])}, "")
	}
	isAsync := func(e Obj) bool {
		d := obj(e["data"])
		return role[str(e["source"])] == "queue" || role[str(e["target"])] == "queue" || reAsyncLabel.MatchString(labelStr(e["label"])) || asyncPresets[labelStr(d["preset"])] || truthy(d["kind"])
	}
	callable := map[string]bool{}
	var callableIDs []string
	for _, n := range nodes {
		if inList(role[str(n["id"])], "service", "gateway", "lb", "cache", "database") {
			callable[str(n["id"])] = true
			callableIDs = append(callableIDs, str(n["id"]))
		}
	}
	adjm := map[string][]string{}
	for _, e := range edges {
		s, t := str(e["source"]), str(e["target"])
		if !isAsync(e) && callable[s] && callable[t] && s != t {
			adjm[s] = append(adjm[s], t)
		}
	}
	for _, comp := range sccs(callableIDs, adjm) {
		if len(comp) < 2 {
			continue
		}
		var cids []string
		for _, n := range nodes {
			if contains(comp, str(n["id"])) {
				cids = append(cids, str(n["id"]))
			}
		}
		var names []string
		for _, id := range cids {
			names = append(names, label(id))
		}
		push("arch/sync-cycle", strings.Join(cids, ","), "Synchronous calls form a cycle: "+strings.Join(names, " → ")+" → "+label(cids[0])+". Make one hop asynchronous or break the loop.", cids, nil, "")
	}
	var monitors, watchable []Obj
	for _, n := range nodes {
		r := role[str(n["id"])]
		if r == "monitoring" {
			monitors = append(monitors, n)
		}
		if inList(r, "service", "database", "cache", "queue", "lb", "gateway") {
			watchable = append(watchable, n)
		}
	}
	if len(watchable) > 0 {
		if len(monitors) == 0 {
			push("arch/no-monitoring", "none", "No monitoring component in this diagram: add Prometheus, CloudWatch or similar and connect it.", nil, nil, "")
		} else {
			for _, n := range watchable {
				id := str(n["id"])
				ok := false
				for _, e := range edges {
					if (str(e["source"]) == id && role[str(e["target"])] == "monitoring") || (str(e["target"]) == id && role[str(e["source"])] == "monitoring") {
						ok = true
						break
					}
				}
				if !ok {
					push("arch/no-monitoring", id, label(id)+" is not connected to any monitoring component.", []string{id}, nil, "")
				}
			}
		}
	}
	for _, n := range nodes {
		for _, w := range list(dataOf(n)["warnings"]) {
			id := str(n["id"])
			push("arch/cluster-profile", id+":"+jsStr(w), label(id)+": "+jsStr(w), []string{id}, nil, "")
		}
	}

	// ---- ERD ----
	var tables []Obj
	for _, n := range nodes {
		if k := dataOf(n)["tableKind"]; str(n["type"]) == "table" && k != "enum" && k != "view" {
			tables = append(tables, n)
		}
	}
	colsOf := func(t Obj) []Obj { return objs(dataOf(t)["columns"]) }
	for _, t := range tables {
		tid := str(t["id"])
		var indexes [][]string
		for _, ix := range objs(dataOf(t)["indexes"]) {
			var cs []string
			for _, c := range list(ix["columns"]) {
				cs = append(cs, jsStr(c))
			}
			indexes = append(indexes, cs)
		}
		var pk []string
		hasPK := false
		for _, c := range colsOf(t) {
			if truthy(c["pk"]) {
				pk = append(pk, jsStr(c["name"]))
				hasPK = true
			}
		}
		pk0 := ""
		hasPk0 := len(pk) > 0
		if hasPk0 {
			pk0 = pk[0]
		}
		for _, c := range colsOf(t) {
			fk := obj(c["fk"])
			if fk == nil {
				continue
			}
			name := jsStr(c["name"])
			var covered bool
			if truthy(c["pk"]) && hasPk0 && pk0 == name {
				covered = true
			} else {
				covered = truthy(c["unique"]) || (hasPk0 && pk0 == name)
				for _, ix := range indexes {
					if len(ix) > 0 && ix[0] == name {
						covered = true
					}
				}
			}
			if !covered {
				push("erd/fk-index", tid+"."+jsStr(c["id"]), label(tid)+"."+name+" references "+jsStr(fk["table"])+" but has no index.", []string{tid}, nil, "Add index on "+name)
			}
		}
		if !hasPK {
			push("erd/no-pk", tid, label(tid)+" has no primary key.", []string{tid}, nil, "Add an id primary key")
		}
	}
	for _, e := range edgesOf(page) {
		r := obj(obj(e["data"])["relation"])
		if r == nil {
			continue
		}
		parent, child := byID[str(e["source"])], byID[str(e["target"])]
		if parent == nil || child == nil || str(parent["type"]) != "table" || str(child["type"]) != "table" {
			continue
		}
		pc, cc := findCol(parent, r["fromCol"]), findCol(child, r["toCol"])
		if pc == nil || cc == nil {
			continue
		}
		pid, cid := str(parent["id"]), str(child["id"])
		if baseType(pc["type"]) != baseType(cc["type"]) {
			push("erd/fk-type", cid+"."+jsStr(cc["id"]), label(cid)+"."+jsStr(cc["name"])+" is "+labelStr(cc["type"])+" but references "+label(pid)+"."+jsStr(pc["name"])+" ("+labelStr(pc["type"])+").", []string{cid, pid}, []string{str(e["id"])}, "Change to "+labelStr(pc["type"]))
		}
		if r["optional"] == false && truthy(cc["nullable"]) && !truthy(cc["pk"]) {
			push("erd/nullable-fk", cid+"."+jsStr(cc["id"]), label(cid)+"."+jsStr(cc["name"])+" allows NULL but the relation to "+label(pid)+" is required.", []string{cid, pid}, []string{str(e["id"])}, "Make "+jsStr(cc["name"])+" NOT NULL")
		}
	}
	style := cfg.Naming
	if style == "" {
		style = "snake_case"
	}
	if style != "any" {
		ok := func(s string) bool {
			if style == "snake_case" {
				return reSnake.MatchString(s)
			}
			return reCamel.MatchString(s)
		}
		for _, t := range nodes {
			if str(t["type"]) != "table" || dataOf(t)["tableKind"] == "enum" {
				continue
			}
			tid := str(t["id"])
			if !ok(jsStr(dataOf(t)["label"])) {
				push("erd/naming", tid, `Table "`+jsStr(dataOf(t)["label"])+`" is not `+style+".", []string{tid}, nil, "")
			}
			for _, c := range colsOf(t) {
				if !ok(jsStr(c["name"])) {
					push("erd/naming", tid+"."+jsStr(c["id"]), `Column "`+jsStr(dataOf(t)["label"])+"."+jsStr(c["name"])+`" is not `+style+".", []string{tid}, nil, "")
				}
			}
		}
	}

	// ---- UML ----
	var classes []Obj
	for _, n := range nodes {
		if str(n["type"]) == "class" {
			classes = append(classes, n)
		}
	}
	var umlEdges []Obj
	for _, e := range edges {
		if obj(obj(e["data"])["uml"]) != nil && str(byID[str(e["source"])]["type"]) == "class" && str(byID[str(e["target"])]["type"]) == "class" {
			umlEdges = append(umlEdges, e)
		}
	}
	inh := map[string][]string{}
	for _, e := range umlEdges {
		if obj(obj(e["data"])["uml"])["kind"] == "inheritance" {
			inh[str(e["source"])] = append(inh[str(e["source"])], str(e["target"]))
		}
	}
	for _, comp := range sccs(ids(classes), inh) {
		self := len(comp) == 1 && contains(inh[comp[0]], comp[0])
		if len(comp) < 2 && !self {
			continue
		}
		var cids, names []string
		for _, n := range nodes {
			if contains(comp, str(n["id"])) {
				cids = append(cids, str(n["id"]))
			}
		}
		for _, id := range cids {
			names = append(names, label(id))
		}
		// the last drawn inheritance edge inside the cycle is the one to cut
		inside := []string{}
		var cut Obj
		for _, e := range umlEdges {
			if obj(obj(e["data"])["uml"])["kind"] == "inheritance" && contains(comp, str(e["source"])) && contains(comp, str(e["target"])) {
				inside = append(inside, str(e["id"]))
				cut = e
			}
		}
		fix := ""
		if cut != nil {
			fix = "Remove " + label(str(cut["source"])) + " → " + label(str(cut["target"]))
		}
		push("uml/inheritance-cycle", strings.Join(cids, ","), "Inheritance cycle: "+strings.Join(names, " → ")+" → "+label(cids[0])+".", cids, inside, fix)
	}
	for _, c := range classes {
		if dataOf(c)["kind"] != "interface" {
			continue
		}
		cid := str(c["id"])
		impl := false
		for _, e := range umlEdges {
			k := obj(obj(e["data"])["uml"])["kind"]
			if str(e["target"]) == cid && (k == "realization" || k == "inheritance") {
				impl = true
			}
		}
		if !impl {
			push("uml/interface-no-impl", cid, "Interface "+label(cid)+" has no implementing class.", []string{cid}, nil, "Add an implementing class")
		}
	}

	// sequence diagrams on the page: participants named like a class count as uses of it
	var seqs []Obj
	for _, n := range nodes {
		if str(n["type"]) == "sequence" {
			seqs = append(seqs, n)
		}
	}
	partName := func(raw any) string {
		t := strings.TrimSpace(labelStr(raw))
		if k := strings.LastIndex(t, ":"); k >= 0 {
			t = t[k+1:]
		}
		return strings.ToLower(strings.TrimSpace(t))
	}
	classByName := map[string]Obj{}
	for _, c := range classes {
		k := strings.ToLower(strings.TrimSpace(labelStr(dataOf(c)["label"])))
		if _, ok := classByName[k]; k != "" && !ok {
			classByName[k] = c
		}
	}
	usedInSeq := map[string]bool{}
	for _, q := range seqs {
		for _, p := range objs(dataOf(q)["participants"]) {
			if c, ok := classByName[partName(p["name"])]; ok {
				usedInSeq[str(c["id"])] = true
			}
		}
	}
	if len(classes) > 1 {
		for _, c := range classes {
			cid := str(c["id"])
			used := false
			for _, e := range edges {
				if str(e["source"]) == cid || str(e["target"]) == cid {
					used = true
				}
			}
			if !used && !usedInSeq[cid] {
				push("uml/unused-class", cid, label(cid)+" has no relations to any other class.", []string{cid}, nil, "Delete "+label(cid))
			}
		}
	}

	// methods of a class: its own plus everything it inherits or realizes
	parents := map[string][]string{}
	for _, e := range umlEdges {
		k := obj(obj(e["data"])["uml"])["kind"]
		if k == "inheritance" || k == "realization" {
			parents[str(e["source"])] = append(parents[str(e["source"])], str(e["target"]))
		}
	}
	methodsOf := func(id string) map[string]bool {
		names := map[string]bool{}
		seen := map[string]bool{}
		var walk func(x string)
		walk = func(x string) {
			if seen[x] {
				return
			}
			seen[x] = true
			for _, o := range objs(dataOf(byID[x])["operations"]) {
				names[strings.TrimSpace(labelStr(o["name"]))] = true
			}
			for _, p := range parents[x] {
				walk(p)
			}
		}
		walk(id)
		return names
	}
	methodOf := func(text any) string {
		t := strings.TrimSpace(labelStr(text))
		t = reSeqNum.ReplaceAllString(t, "")
		if eq := strings.Index(t, "="); eq >= 0 && !strings.Contains(t[:eq], "(") {
			t = strings.TrimSpace(t[eq+1:])
		}
		t = reSeqNew.ReplaceAllString(t, "")
		m := reSeqCall.FindStringSubmatchIndex(t)
		if m == nil {
			return ""
		}
		name := t[m[2]:m[3]]
		if m[4] >= 0 || reSeqLower.MatchString(name) {
			return name
		}
		return ""
	}
	var messagesOf func(items []Obj, acc []Obj) []Obj
	messagesOf = func(items []Obj, acc []Obj) []Obj {
		for _, it := range items {
			switch str(it["type"]) {
			case "message":
				acc = append(acc, it)
			case "fragment":
				for _, sec := range objs(it["sections"]) {
					acc = messagesOf(objs(sec["items"]), acc)
				}
			}
		}
		return acc
	}
	for _, q := range seqs {
		qid := str(q["id"])
		parts := map[string]Obj{}
		for _, p := range objs(dataOf(q)["participants"]) {
			parts[str(p["id"])] = p
		}
		seen := map[string]bool{}
		for _, m := range messagesOf(objs(dataOf(q)["items"]), nil) {
			if k := m["kind"]; k == "return" || k == "create" || k == "destroy" {
				continue
			}
			target, hasT := parts[str(m["to"])]
			var cls Obj
			if hasT {
				cls = classByName[partName(target["name"])]
			}
			method := methodOf(m["text"])
			if cls == nil || method == "" || dataOf(cls)["kind"] == "enum" {
				continue
			}
			key := qid + ":" + str(m["to"]) + ":" + method
			if seen[key] {
				continue
			}
			seen[key] = true
			cid := str(cls["id"])
			if !methodsOf(cid)[method] {
				push("uml/seq-missing-method", key, label(qid)+`: message "`+strings.TrimSpace(labelStr(m["text"]))+`" calls `+method+"() on "+jsStr(target["name"])+", but class "+label(cid)+" has no such method.", []string{qid, cid}, nil, "Add "+method+"() to "+label(cid))
			}
		}
	}

	// object diagram: every slot of an object names an attribute of its class (own or inherited)
	attrsOf := func(id string) map[string]bool {
		names := map[string]bool{}
		seen := map[string]bool{}
		var walk func(x string)
		walk = func(x string) {
			if seen[x] {
				return
			}
			seen[x] = true
			for _, a := range objs(dataOf(byID[x])["attributes"]) {
				names[strings.TrimSpace(labelStr(a["name"]))] = true
			}
			for _, p := range parents[x] {
				walk(p)
			}
		}
		walk(id)
		return names
	}
	for _, o := range nodes {
		if str(o["type"]) != "object" {
			continue
		}
		oid := str(o["id"])
		cls := classByName[strings.ToLower(strings.TrimSpace(labelStr(dataOf(o)["className"])))]
		if cls == nil || dataOf(cls)["kind"] == "enum" {
			continue
		}
		cid := str(cls["id"])
		known := attrsOf(cid)
		seen := map[string]bool{}
		for _, sl := range objs(dataOf(o)["slots"]) {
			name := strings.TrimSpace(labelStr(sl["name"]))
			if name == "" || known[name] || seen[name] {
				continue
			}
			seen[name] = true
			push("uml/object-unknown-slot", oid+":"+name, "Object "+label(oid)+": slot \""+name+"\" is not an attribute of class "+label(cid)+".", []string{oid, cid}, nil, "")
		}
	}

	rank := map[string]int{"error": 0, "warning": 1, "info": 2}
	order := map[string]int{}
	for i, r := range LintRules {
		order[r.ID] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		if order[a.Rule] != order[b.Rule] {
			return order[a.Rule] < order[b.Rule]
		}
		return a.ID < b.ID
	})
	return out
}
