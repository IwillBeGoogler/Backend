// Writes golden.json: fixtures and the output of the TypeScript core on them.
// Run: TS_CORE_DIR=/path/to/the/typescript/core ../gen/gen-golden.sh   (needs tsx; TS_CORE_DIR is the folder with dsl, exporters, layout, lint, catalog)
import { writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
const coreDir = process.env.TS_CORE_DIR;
if (!coreDir) throw new Error('set TS_CORE_DIR to the TypeScript core folder (dsl, exporters, layout, lint, catalog)');
const core = (name: string) => import(pathToFileURL(resolve(coreDir, name)).href);
const { applyDsl, parseDsl, printPage } = await core('dsl/index.ts');
const { exportPage, EXPORT_FORMATS } = await core('exporters.ts');
const { applyLayout, LAYOUTS } = await core('layout.ts');
const { lintPage } = await core('lint.ts');
const { searchCatalog, resolveIcon } = await core('catalog.ts');

const here = dirname(fileURLToPath(import.meta.url));
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v));
const n = (id: string, type: string, x: number, y: number, data: any, extra: any = {}) => ({ id, type, position: { x, y }, data, ...extra });
const e = (id: string, source: string, target: string, extra: any = {}) => ({ id, source, target, ...extra });

const arch = {
  id: 'p1', name: 'Shop architecture',
  nodes: [
    n('user', 'icon', 0, 0, { label: 'User', iconId: 'user' }),
    n('cdn', 'icon', 200, 0, { label: 'CDN', iconId: 'cdn' }),
    n('lb', 'icon', 400, 0, { label: 'Load balancer', iconId: 'load-balancer' }),
    n('vpc', 'group', 100, 200, { label: 'VPC', groupIcon: 'aws-group-aws-cloud' }, { width: 700, height: 400 }),
    n('api1', 'icon', 30, 60, { label: 'API 1', iconId: 'service', subtitle: ':8080' }, { parentId: 'vpc' }),
    n('api2', 'icon', 200, 60, { label: 'API 2', iconId: 'service' }, { parentId: 'vpc' }),
    n('inner', 'group', 40, 200, { label: 'Data' }, { parentId: 'vpc', width: 400, height: 160 }),
    n('db', 'icon', 20, 50, { label: 'Orders DB', iconId: 'postgresql' }, { parentId: 'inner' }),
    n('cache', 'icon', 200, 50, { label: 'Cache', iconId: 'redis' }, { parentId: 'inner' }),
    n('q', 'icon', 900, 100, { label: 'Events', iconId: 'kafka' }),
    n('mon', 'icon', 900, 300, { label: 'Prometheus', iconId: 'prometheus' }),
    n('bk', 'icon', 900, 500, { label: 'Backups', iconId: 'object-storage' }),
    n('svc-a', 'icon', 0, 700, { label: 'Billing', iconId: 'service' }),
    n('svc-b', 'icon', 200, 700, { label: 'Ledger', iconId: 'service' }),
    n('note', 'sticky', 500, 700, { label: 'todo' }, { width: 140, height: 100 }),
  ],
  edges: [
    e('e1', 'user', 'cdn'), e('e2', 'cdn', 'lb'), e('e3', 'lb', 'api1', { label: 'round robin' }), e('e4', 'user', 'api2'),
    e('e5', 'api1', 'db'), e('e6', 'api2', 'db', { data: { dashed: true } }), e('e7', 'api1', 'cache'), e('e8', 'api1', 'q', { label: 'publish', data: { preset: 'traces' } }),
    e('e9', 'mon', 'api1', { data: { arrows: 'both', color: '#ff0000' } }), e('e10', 'db', 'bk', { label: 'nightly backup' }),
    e('e11', 'svc-a', 'svc-b', { label: 'charge' }), e('e12', 'svc-b', 'svc-a', { label: 'refund' }), e('e13', 'api2', 'svc-a'),
  ],
};

const col = (id: string, name: string, type: string, extra: any = {}) => ({ id, name, type, pk: false, unique: false, nullable: true, ...extra });
const tbl = (id: string, label: string, x: number, y: number, columns: any[], extra: any = {}) => n(id, 'table', x, y, { label, columns, indexes: [], checks: [], accent: '#2f6fdb', collapsed: false, ...extra }, { width: 260, height: 40 + columns.length * 28 + 6 });
const rel = (id: string, s: string, t: string, fromCol: string, toCol: string, cardinality: string, optional = false, identifying = false) => e(id, s, t, { data: { relation: { fromCol, toCol, cardinality, optional, identifying } } });
const erd = {
  id: 'p2', name: 'Shop schema',
  nodes: [
    tbl('t-users', 'users', 0, 0, [col('c0', 'id', 'uuid', { pk: true, nullable: false }), col('c1', 'email', 'varchar(255)', { unique: true, nullable: false }), col('c2', 'createdAt', 'timestamptz', { default: 'now()' }), col('c3', 'bio', 'text', { comment: 'free text' })]),
    tbl('t-orders', 'orders', 400, 0, [col('c0', 'id', 'bigint', { pk: true, nullable: false, generated: 'autoincrement' }), col('c1', 'user_id', 'int', { nullable: true, fk: { table: 'users', column: 'id' } }), col('c2', 'status', 'text', { default: "'new'" }), col('c3', 'total', 'numeric(10,2)', { nullable: false })], { indexes: [{ columns: ['status', 'total'], unique: false, name: 'idx_status' }] }),
    tbl('t-items', 'order_items', 800, 0, [col('c0', 'order_id', 'bigint', { pk: true, nullable: false, fk: { table: 'orders', column: 'id', onDelete: 'CASCADE' } }), col('c1', 'product_id', 'int', { pk: true, nullable: false, fk: { table: 'products', column: 'id' } }), col('c2', 'qty', 'int', { nullable: false })]),
    tbl('t-products', 'products', 800, 300, [col('c0', 'id', 'int', { pk: true, nullable: false }), col('c1', 'name', 'text', { nullable: false })], { indexes: [{ columns: ['name'], unique: true }] }),
    tbl('t-log', 'audit_log', 0, 400, [col('c0', 'line', 'text')]),
    tbl('t-status', 'status', 400, 400, [col('c0', 'new', ''), col('c1', 'paid', '')], { tableKind: 'enum' }),
  ],
  edges: [
    rel('r1', 't-users', 't-orders', 'c0', 'c1', '1:N', false),
    rel('r2', 't-orders', 't-items', 'c0', 'c0', '1:N', false, true),
    rel('r3', 't-products', 't-items', 'c0', 'c1', '1:N', false, true),
  ],
};

const cls = (id: string, label: string, x: number, y: number, kind: string, attributes: any[] = [], operations: any[] = []) => n(id, 'class', x, y, { label, kind, attributes, operations }, { width: 200, height: 120 });
const uml = (id: string, s: string, t: string, kind: string) => e(id, s, t, { data: { uml: { kind } } });
const mixed = {
  id: 'p3', name: 'Domain "model"',
  nodes: [
    cls('Animal', 'Animal', 0, 0, 'abstract', [{ name: 'name', type: 'string', visibility: '+' }], [{ name: 'speak', visibility: '+', params: [{ name: 'loud' }, { name: 'times' }] }]),
    cls('Dog', 'Dog', 300, 0, 'class', [{ name: 'breed', type: 'string' }]),
    cls('Pet', 'Pet', 600, 0, 'interface'),
    cls('Loop1', 'Loop1', 0, 300, 'class'), cls('Loop2', 'Loop2', 300, 300, 'class'),
    cls('Lonely', 'Lonely', 600, 300, 'class'),
    n('seq1', 'sequence', 0, 600, { label: 'Checkout', accent: '#ff8800', autonumber: true, participants: [{ id: 'web', name: 'Web', kind: 'actor' }, { id: 'api', name: 'api', kind: 'participant' }, { id: 'db', name: 'DB', kind: 'database' }], items: [
      { type: 'message', id: 'm1', from: 'web', to: 'api', text: 'POST /order', kind: 'sync' },
      { type: 'fragment', id: 'f1', op: 'alt', sections: [{ id: 's1', guard: 'stock ok', items: [{ type: 'message', id: 'm2', from: 'api', to: 'db', text: 'insert', kind: 'async' }, { type: 'note', id: 'n1', text: 'saved', over: ['db'], side: 'right' }] }, { id: 's2', guard: 'else', items: [{ type: 'message', id: 'm3', from: 'api', to: 'web', text: 'sold out', kind: 'return' }] }] },
    ] }, { width: 500, height: 300 }),
  ],
  edges: [uml('u1', 'Dog', 'Animal', 'inheritance'), uml('u2', 'Loop1', 'Loop2', 'inheritance'), uml('u3', 'Loop2', 'Loop1', 'inheritance'), uml('u4', 'Dog', 'Pet', 'association')],
};
const sm = (id: string, label: string, kind: string, x: number, y: number, extra: any = {}) => n(id, 'state', x, y, { label, stateKind: kind, accent: '#2f6fdb' }, { width: 110, height: 40, ...extra });
const umlPage = {
  id: 'p4', name: 'UML page',
  nodes: [
    cls('Svc', 'Svc', 0, 0, 'class', [{ id: 'a0', name: 'id', type: 'int', visibility: '-' }], [{ id: 'o0', name: 'run', visibility: '+', type: 'void', params: [] }]),
    cls('Repo', 'Repo', 300, 0, 'interface', [], [{ id: 'o0', name: 'find', visibility: '+', type: 'Svc', params: [{ name: 'id', type: 'int' }], static: true }]),
    n('pk', 'group', 0, 200, { label: 'Core', variant: 'package' }, { width: 400, height: 200 }),
    cls('Inner', 'Inner', 20, 40, 'enum', [{ id: 'a0', name: 'ONE', visibility: '' }]),
    sm('st1', 'Idle', 'state', 0, 500), sm('st2', 'Busy', 'state', 200, 500, { data: { label: 'Busy', stateKind: 'state', accent: '#2f6fdb', activity: 'entry / go' } }),
    n('cmp1', 'group', 500, 400, { label: 'Both', variant: 'composite' }, { width: 300, height: 200 }),
    sm('st3', 'Inner', 'state', 20, 60, { parentId: 'cmp1' }),
    n('uc1', 'usecase', 0, 700, { label: 'Buyer', ucKind: 'actor', accent: '#0f9d8f' }, { width: 70, height: 100 }),
    n('uc2', 'usecase', 200, 700, { label: 'Buy', ucKind: 'usecase', accent: '#0f9d8f', extensionPoints: ['Pay'] }, { width: 150, height: 101 }),
    n('co1', 'component', 0, 900, { label: 'Web', compKind: 'component', accent: '#8e5bd6', ports: [{ id: 'p1', name: 'IWeb', kind: 'provided', side: 'left', offset: 0.5 }] }, { width: 150, height: 72 }),
    n('co2', 'component', 300, 900, { label: 'Db', compKind: 'node', accent: '#8e5bd6', archRef: { nodeId: 'db', fileId: 'f2' }, ports: [{ id: 'p1', name: 'IWeb', kind: 'required', side: 'right', offset: 0.5 }] }, { width: 150, height: 90 }),
  ],
  edges: [
    uml('x1', 'Svc', 'Repo', 'dependency'), e('x2', 'Svc', 'Repo', { data: { uml: { kind: 'association', directed: true, sourceMult: '1', targetMult: '*', sourceRole: 'svc' } } }),
    uml('t1', 'st1', 'st2', 'transition'), e('t2', 'st2', 'st3', { label: 'go', data: { uml: { kind: 'transition' } } }),
    uml('u1', 'uc1', 'uc2', 'association'), e('u2', 'uc2', 'uc1', { label: '«include»', data: { uml: { kind: 'include' } } }),
    e('c1', 'co2', 'co1', { data: { uml: { kind: 'assembly', sourcePort: 'p1', targetPort: 'p1' } } }), uml('c2', 'co1', 'co2', 'dependency'),
  ],
};
const act = (id: string, label: string, actKind: string, x: number, y: number, extra: any = {}, data: any = {}) => n(id, 'activity', x, y, { label, actKind, accent: '#d9822b', ...data }, { width: 120, height: 48, ...extra });
const actPage = {
  id: 'p5', name: 'Activity page',
  nodes: [
    n('ln1', 'group', 0, 0, { label: 'Customer', variant: 'lane', laneOrient: 'v', accent: '#d9822b', stroke: '#d9822b', fillOpacity: 3, border: true }, { width: 240, height: 520 }),
    n('ln2', 'group', 300, 0, { label: 'Shop', variant: 'lane', laneOrient: 'h', accent: '#d9822b', stroke: '#d9822b', fillOpacity: 3, border: true }, { width: 640, height: 200 }),
    act('i0', '', 'initial', 40, 60, { parentId: 'ln1', width: 28, height: 28 }),
    act('a1', 'Place order', 'action', 20, 120, { parentId: 'ln1' }),
    act('o1', 'Order', 'object', 20, 220, { parentId: 'ln1', width: 110, height: 62 }, { objectState: 'placed' }),
    act('d1', 'in stock?', 'decision', 40, 60, { parentId: 'ln2', width: 56, height: 56 }),
    act('f1', '', 'fork', 140, 60, { parentId: 'ln2', width: 8, height: 120 }, { vertical: true }),
    act('s1', 'Notify', 'send', 200, 60, { parentId: 'ln2', width: 130, height: 48 }),
    act('n1', 'retry twice', 'note', 700, 300, { width: 140, height: 56 }),
    act('e1', '', 'final', 700, 400, { width: 36, height: 36 }),
  ],
  edges: [
    e('f1e', 'i0', 'a1', { data: { uml: { kind: 'transition' } } }),
    e('f2e', 'a1', 'd1', { label: '[ok]', data: { uml: { kind: 'transition' } } }),
    e('f3e', 'd1', 'e1', { label: '[no]', data: { uml: { kind: 'transition' } } }),
    e('f4e', 'n1', 'a1', { data: { uml: { kind: 'note' } } }),
  ],
};
const dep = (id: string, label: string, depKind: string, x: number, y: number, extra: any = {}, data: any = {}) => n(id, 'deployment', x, y, { label, depKind, accent: '#3f8f6b', ...data }, { width: 160, height: 96, ...extra });
const depPage = {
  id: 'p6', name: 'Deployment page',
  nodes: [
    dep('phone', 'Phone', 'device', 0, 0, { width: 260, height: 200 }, { stereotype: 'mobile', container: true }),
    dep('app', 'app.apk', 'artifact', 40, 60, { parentId: 'phone', width: 130, height: 72 }),
    dep('srv', 'Server', 'node', 400, 0, { width: 360, height: 260 }, { iconId: 'service', container: true }),
    dep('env', 'Tomcat', 'executionEnvironment', 20, 50, { parentId: 'srv', width: 300, height: 190 }, { container: true }),
    dep('war', 'shop.war', 'artifact', 20, 50, { parentId: 'env', width: 130, height: 72 }),
    dep('shop', 'Shop', 'component', 150, 100, { parentId: 'env', width: 150, height: 72 }),
  ],
  edges: [
    e('p1', 'phone', 'srv', { label: 'HTTPS', data: { uml: { kind: 'communication' } } }),
    e('p2', 'war', 'srv', { label: '«deploy»', data: { uml: { kind: 'deploy' } } }),
    e('p3', 'war', 'shop', { label: '«manifest»', data: { uml: { kind: 'manifest' } } }),
    e('p4', 'app', 'phone', { label: 'custom', data: { uml: { kind: 'deploy' } } }),
  ],
};
const pkgG = (id: string, label: string, x: number, y: number, extra: any = {}) => n(id, 'group', x, y, { label, variant: 'package', stroke: '#4b5563', accent: '#4b5563', fillOpacity: 0, border: true, strokeWidth: 1.5, strokeStyle: 'solid' }, { width: 300, height: 200, ...extra });
const clsN = (id: string, label: string, x: number, y: number, attrs: string[], extra: any = {}, data: any = {}) => n(id, 'class', x, y, { label, kind: 'class', accent: '#2f6fdb', attributes: attrs.map((a, i) => ({ id: `a${i}`, visibility: '-', name: a, type: 'String' })), operations: [], ...data }, { width: 160, height: 80, ...extra });
const pkgPage = {
  id: 'p7', name: 'Package page',
  nodes: [
    pkgG('web', 'web', 0, 0, { width: 360, height: 240 }),
    pkgG('ctl', 'controllers', 20, 50, { parentId: 'web', width: 300, height: 160 }),
    clsN('oc', 'OrderController', 20, 50, ['svc'], { parentId: 'ctl' }),
    pkgG('domain', 'domain', 500, 0),
    clsN('ord', 'Order', 20, 50, ['id', 'total'], { parentId: 'domain' }),
    pkgG('util', 'util', 500, 300),
  ],
  edges: [
    e('r1', 'web', 'domain', { label: '«import»', data: { uml: { kind: 'import' } } }),
    e('r2', 'domain', 'util', { label: '«access»', data: { uml: { kind: 'access' } } }),
    e('r3', 'web', 'util', { label: 'shared', data: { uml: { kind: 'merge' } } }),
    e('r4', 'ctl', 'domain', { data: { uml: { kind: 'dependency' } } }),
  ],
};
const obj = (id: string, label: string, className: string | undefined, x: number, y: number, slots: [string, string][], extra: any = {}) => n(id, 'object', x, y, { accent: '#0e7490', label, ...(className ? { className } : {}), slots: slots.map(([name, value], i) => ({ id: `s${i}`, name, value })) }, { width: 160, height: 80, ...extra });
const objPage = {
  id: 'p8', name: 'Object page',
  nodes: [
    clsN('cc', 'Customer', 0, 0, ['name', 'email']),
    clsN('cp', 'VipCustomer', 0, 200, ['tier']),
    clsN('ce', 'Color', 300, 0, [], {}, { kind: 'enum' }),
    obj('alice', 'alice', 'Customer', 400, 0, [['name', '"Alice"'], ['phone', '123'], ['phone', '456']]),
    obj('bob', 'bob', 'VipCustomer', 400, 200, [['tier', 'gold'], ['email', '"b@x.io"'], ['age', '7']]),
    obj('anon', 'anon', 'Ghost', 400, 400, [['x', '1']]),
    obj('red', 'red', 'Color', 700, 0, [['rgb', 'ff0000']]),
  ],
  edges: [
    e('i1', 'cp', 'cc', { data: { uml: { kind: 'inheritance' } } }),
    e('k1', 'alice', 'bob', { label: 'friend', data: { uml: { kind: 'link', sourceRole: 'a', targetRole: 'b' } } }),
  ],
};
const commPage = {
  id: 'p9', name: 'Communication page',
  nodes: [
    obj('alice', 'alice', 'Customer', 0, 0, []),
    obj('bob', 'bob', 'Shop', 300, 0, []),
    obj('anon', 'anon', undefined, 600, 0, []),
  ],
  edges: [
    e('k1', 'alice', 'bob', { label: '1: login() →\n3: done ←', data: { uml: { kind: 'link', messages: [{ id: 'm1', seq: '1', text: 'login()', dir: 'fwd' }, { id: 'm3', seq: '3', text: 'done', dir: 'back' }] } } }),
    e('k2', 'bob', 'anon', { label: '2: ping ⇢\n2.1: ack ←', data: { uml: { kind: 'link', sourceRole: 'r', messages: [{ id: 'm2', seq: '2', text: 'ping', dir: 'fwd', async: true }, { id: 'm4', seq: '2.1', text: 'ack', dir: 'back' }] } } }),
  ],
};
const timingPage = {
  id: 'p10', name: 'Timing page',
  nodes: [
    n('web', 'timing', 0, 0, {
      label: 'Web request', accent: '#c2410c', unit: 'ms', end: 400,
      lifelines: [
        { id: 'wb', name: 'Web browser', kind: 'robust', states: ['Idle', 'Waiting', 'Not found'] },
        { id: 'wu', name: 'wu', kind: 'concise', states: ['Idle', 'Waiting'] },
        { id: 'sw', name: 'Switch', kind: 'binary', states: ['high', 'low'] },
        { id: 'ck', name: 'Clock', kind: 'clock', states: [], period: 50 },
      ],
      changes: [
        { id: 'c1', life: 'wb', t: 0, state: 'Idle' }, { id: 'c2', life: 'wu', t: 0, state: 'Idle' }, { id: 'c3', life: 'sw', t: 0, state: 'low' },
        { id: 'c4', life: 'wu', t: 100, state: 'Waiting' }, { id: 'c5', life: 'wb', t: 100, state: 'Waiting' }, { id: 'c6', life: 'sw', t: 150.5, state: 'high' }, { id: 'c7', life: 'wb', t: 300, state: 'Not found' },
      ],
      messages: [{ id: 'm1', from: 'wu', to: 'wb', t: 100, t2: 150, text: 'URL' }, { id: 'm2', from: 'wb', to: 'wu', t: 300, text: '' }],
      durations: [{ id: 'k1', life: 'wb', t1: 100, t2: 300, text: '{200 ms}' }],
    }, { width: 728, height: 320 }),
  ],
  edges: [],
};
const fixtures: Record<string, any> = { arch, erd, mixed };

const texts: Record<string, string> = {
  arch: `#!sdd 1
diagram "Web app"
// comment
node user "User" icon=user
node cdn "CDN" icon=cdn sub="edge"
node lb "Load balancer" icon=load-balancer
group vpc "VPC" icon=aws-group-aws-cloud {
  node api "API" icon=service sub=":8080"
  node api2 "API 2" icon=service
  group data "Data" {
    node db "Postgres" icon=postgresql
    node cache "Cache" icon=nope-not-here
  }
}
node mon "Metrics" icon=prometheus

user -> cdn
cdn -> lb "https"
lb -> api "rr" preset=metrics
lb -> api2 dashed
api <-> db color=#ff00aa
api -- cache
mon <- api
user -> cdn "second"
`,
  er: `#!sdd 1
diagram "Shop"
table users {
  id uuid [pk]
  email varchar(255) [not null, unique]
  created_at timestamptz [default: now()]
  note: "the people"
}
table orders {
  id bigint [pk, increment]
  user_id uuid [not null, ref: > users.id]
  status text [default: "new", note: "state"]
  total numeric(10, 2) [not null]
  indexes {
    status
    (user_id, status) [unique, name: "ux_user_status"]
  }
}
enum status {
  new
  paid [note: x]
}
view stats {
  n int
}
table items {
  order_id bigint [pk]
  tag text
}
ref: items.order_id > orders.id [delete: cascade, update: set null]
ref: items.tag <> users.email
`,
  seq: `#!sdd 1
diagram "Flows"
seq checkout "Checkout" {
  autonumber
  actor web as "Web client" model=m_web
  participant api model=m_api
  database db
  web -> api: POST /order
  alt stock ok
    api ->> db: insert
    note right of db: saved
  else sold out
    api --> web: no
  end
  loop every 5s
    api -x db: ping
  end
}
seq "Second flow" {
  a -> b: hi
}
`,
  classes: `#!sdd 1
diagram "Domain"
classes {
  package shop "Shop" {
    abstract class Animal<T, K extends Key> "entity" archRef=svc archFile=f1 model=m_animal {
      - static count: int = 0
      # name: string
      + abstract speak(loud: bool, times): void
    }
    interface Pet {
      + play(): void
    }
    enum Color {
      RED
      GREEN
    }
  }
  class Dog extends Animal implements Pet model=m_dog {
    + breed: string
  }
  class Lonely
  Dog --o Pet "walks" srcMult=1 dstMult=0..* srcRole=owner dstRole=pets
  Dog --> Color
  Dog ..> Animal
  Animal --* Pet
}
`,
  states: `#!sdd 1
diagram "Machine"
states {
  initial start
  state idle "Idle" activity="entry / start timer\nexit / stop"
  state active "Active" {
    state a1 "A1"
    region
    state a2 "A2"
    history h
    entryPoint ep
    exitPoint xp "Done"
  }
  choice c
  fork f vertical
  join j
  final end
  start -> idle
  idle -> ep "go [ok] / run"
  ep -> a1
  a2 -> xp
  active -> c
  c -> end "done"
}
`,
  usecases: `#!sdd 1
diagram "Shop uses"
usecases {
  actor customer "Customer"
  actor vip "VIP"
  boundary shop "Shop" {
    usecase checkout "Checkout" {
      point "Pay by card"
      point "Pay by cash"
    }
    usecase pay "Pay"
    usecase cancel "Cancel"
  }
  customer -- checkout srcMult=1 dstMult=*
  checkout include pay
  cancel extend checkout "if unpaid"
  vip generalizes customer
}
`,
  components: `#!sdd 1
diagram "Services"
components {
  node server "Server" {
    component api "API" stereotype=service archRef=db {
      provides IOrders
      requires IPay
    }
    component pay "Pay" {
      provides IPay
    }
  }
  artifact jar "app.jar"
  api ..> jar "deploys"
  jar -- api
}
`,
  activity: `#!sdd 1
diagram "Order"
activity {
  initial start
  lane cust "Customer" {
    action order "Place order"
    object o "Order" state=placed
  }
  lane shop "Shop" orient=h {
    action check
    decision d
    fork f vertical
    send s "Notify"
    accept a "Reply"
  }
  note n "retry twice"
  final end
  flowFinal ff
  start -> order
  order -> check
  check -> d
  d -> end "[in stock]"
  d -> ff "[out of stock]"
  f -> s
  s -> a "[sent]"
  n -- check
}
`,
  deployment: `#!sdd 1
diagram "Deployment"
node web "Web" icon=browser
deployment {
  device phone "Phone" stereotype=mobile {
    artifact app "app.apk"
  }
  node server "Server" icon=postgresql {
    executionEnvironment tomcat "Tomcat" {
      artifact war "shop.war"
      component shop "Shop"
    }
  }
  node ghost "Ghost" icon=nope-not-here
  phone -- server "HTTPS"
  war ..> server
  war manifests shop
  app ..> phone "custom label"
}
`,
  packages: `#!sdd 1
diagram "Packages"
classes {
  package web "Web" {
    package ctl "Controllers" {
      class OrderController {
        - svc: OrderService
      }
    }
  }
  package domain "Domain" {
    class Order
  }
  package util "Util" {
  }
}
packages {
  web imports domain
  domain access util "uses"
  web merges util
  ctl ..> domain
  web imports util "«custom»"
}
`,
  packagesonly: `#!sdd 1
packages {
  package a "A" {
    package b "B"
  }
  package c "C"
  a imports c
  b ..> c "dep"
}
`,
  objects: `#!sdd 1
diagram "Objects"
classes {
  class Customer {
    - name: String
    - email: String
  }
}
objects {
  object alice "alice" class=Customer model=m_customer {
    name = "Alice"
    email = "a@x.io"
    nickname
    "full name" = "A B"
    note = "x # y"
  }
  object bob class=Customer
  object anon
  alice -- bob "friend" srcRole=a dstRole=b
  bob -- alice
}
`,
  communication: `#!sdd 1
diagram "Comm"
classes {
  class Customer
}
communication {
  object user "user" class=Customer
  object web "web server" class=WebApp {
    port = 8080
  }
  object auth
  user -- web srcRole=caller
  user -- auth "unused"
  1 user -> web "login(pw)"
  2 web -> auth "authenticate()"
  2.1 auth -> web "audit()" async
  3 auth -> web "token"
  4 web -> user
}
`,
  commbroken: `#!sdd 1
communication {
  object a
  object a "dup"
  1 a -> ghost "x"
  x a -> a "bad number"
  2 a ~> a
  3 a -> a "ok" colour=red
  frob
`,
  timing: `#!sdd 1
diagram "Timing"
timing t1 "Web request" {
  unit ms
  end 400
  robust wb "Web browser" states Idle, Waiting, "Not found"
  concise wu "Web user"
  binary sw "Switch"
  clock ck "Clock" period=50
  wb @0 Idle
  wu @0 Idle
  sw @0 low
  wu @100 Waiting
  wb @100 Waiting
  sw @150.5 high
  wb @300 "Not found"
  wu -> wb @100..150 "URL"
  wb -> wu @300
  wb 100..300 "{200 ms}"
}
timing t2 {
  robust a
  a @0 s
  a @80 t
  a 10..20
}
`,
  timingbroken: `#!sdd 1
timing x "X" {
  robust a
  a @0 s
  b @5 s
  a -> ghost @5
  a @x s
  clock c
  c @1 s
  binary d
  d @1 maybe
  frob 1
  end -3
  a 1..2..3
  robust a
  robust
  clock k period=zero foo
  binary z states x, y
  unit
timing x "dup" {
}
timing {
`,
  pkgbroken: `#!sdd 1
packages {
  package a
  package a "dup"
  a imports ghost
  a ..> a
  frob
  package {
}
`,
  objbroken: `#!sdd 1
objects {
  object o1 class=X colour=red {
    = oops
    ok = 1
  }
  object o1 "dup"
  object
  o1 -- ghost
  o1 --> o1
  o1 -- o1 foo=bar
  frob x
`,
  actbroken: `#!sdd 1
activity {
  lane l "L" {
    lane m "M" {
    }
    action a
    action a
    bogus x
    initial
  }
  lane q "Q" orient=z color=red {
  }
  a -> ghost
  ghost -- a
  note n "x" {
  }
  action z
`,
  depbroken: `#!sdd 1
deployment {
  artifact a "A" {
  }
  node n "N" colour=red icon=nope {
    bogus
  }
  n -- missing
  a manifests n
  frob
`,
  umlall: `#!sdd 1
diagram "Everything"
node web "Web" icon=browser
classes {
  class A {
    + x: int
  }
}
states {
  state s "S"
}
usecases {
  actor u "U"
}
components {
  component c "C"
}
`,
  umlbroken: `#!sdd 1
classes {
  class A {
    + (
  }
  A --|> B
  frob
}
states {
  a -> b
  region
}
usecases {
  actor a "A"
  actor a "B"
  actor x {
}
components {
  bogus x
  provides I
`,
  broken: `#!sdd 1
diagram "Oops"
node a "A" icon=user
node a "Dup"
node b "B" extra=1 ,
a -> ghost
frobnicate everything
group g "G" {
  node c "C" {
}
table t {
  id int [wat]
  id int
`,
  broken2: `node x "unterminated
table y {
  a.b > c.d
}
x -> 
seq s {
  end
  hello world
`,
};

// ---- cases ----
const num = (v: unknown, d = 0) => (typeof v === 'number' ? v : d);
void num;
const cases: any = { layout: [], dslApply: [], dslParse: [], print: [], lint: [], export: [], search: [], resolve: [] };

for (const [fx, page] of Object.entries(fixtures)) {
  for (const algo of LAYOUTS) {
    for (const opts of [{}, { direction: 'TB' as const, gap: 40 }, ...(algo === 'force' ? [{ seed: 7 }] : []), ...(algo === 'tree' || algo === 'radial' ? [{ rootId: page.nodes[1].id }] : [])]) {
      const p = clone(page);
      const moved = applyLayout(p, algo, opts);
      cases.layout.push({ name: `${fx}/${algo}/${JSON.stringify(opts)}`, page, algo, opts, expected: { nodes: p.nodes, edges: p.edges, moved: moved.length } });
    }
  }
  const sel = page.nodes.filter((x: any) => !x.parentId).slice(0, 3).map((x: any) => x.id);
  const p = clone(page);
  const moved = applyLayout(p, 'grid', { only: sel });
  cases.layout.push({ name: `${fx}/only`, page, algo: 'grid', opts: { only: sel }, expected: { nodes: p.nodes, edges: p.edges, moved: moved.length } });

  cases.print.push({ name: fx, page, expected: printPage(clone(page)) });
  for (const [cfgName, cfg] of Object.entries({ none: {}, custom: { rules: { 'arch/spof': { enabled: false }, 'erd/no-pk': { severity: 'info' } }, naming: 'camelCase' }, any: { naming: 'any' } })) {
    cases.lint.push({ name: `${fx}/${cfgName}`, page, cfg, expected: lintPage(clone(page), cfg as any).map(({ fix, ...r }) => ({ ...r, fix: fix?.label })) });
  }
  for (const f of EXPORT_FORMATS) cases.export.push({ name: `${fx}/${f}`, page, format: f, expected: exportPage(clone(page), f) });
}

for (const [name, text] of Object.entries(texts)) {
  const pr = parseDsl(text);
  cases.dslParse.push({ name, text, expected: pr.diagnostics });
  const r = applyDsl(undefined, text);
  cases.dslApply.push({ name, text, page: null, expected: { page: r.page, diagnostics: r.diagnostics, added: r.added, removed: r.removed } });
  if (!r.diagnostics.some((d) => d.severity === 'error')) cases.print.push({ name: `roundtrip/${name}`, page: r.page, expected: printPage(clone(r.page)) });
}
// sync onto existing pages
const edit = (text: string, f: (s: string) => string) => f(text);
const archText = printPage(clone(arch));
const erdText = printPage(clone(erd));
const mixedText = printPage(clone(mixed));
const umlText = printPage(clone(umlPage));
const actText = printPage(clone(actPage));
const depText = printPage(clone(depPage));
cases.print.push({ name: 'actPage', page: actPage, expected: actText });
cases.print.push({ name: 'depPage', page: depPage, expected: depText });
cases.print.push({ name: 'umlPage', page: umlPage, expected: umlText });
const pkgText = printPage(clone(pkgPage));
const objText = printPage(clone(objPage));
const commText = printPage(clone(commPage));
const timingText = printPage(clone(timingPage));
cases.print.push({ name: 'pkgPage', page: pkgPage, expected: pkgText });
cases.print.push({ name: 'objPage', page: objPage, expected: objText });
cases.print.push({ name: 'commPage', page: commPage, expected: commText });
cases.print.push({ name: 'timingPage', page: timingPage, expected: timingText });
for (const [fx, page] of [['pkgPage', pkgPage], ['objPage', objPage], ['commPage', commPage], ['timingPage', timingPage], ['depPage', depPage], ['actPage', actPage]] as const) {
  for (const [cfgName, cfg] of Object.entries({ none: {}, custom: { rules: { 'uml/object-unknown-slot': { severity: 'error' } } }, off: { rules: { 'uml/object-unknown-slot': { enabled: false } } } })) {
    cases.lint.push({ name: `${fx}/${cfgName}`, page, cfg, expected: lintPage(clone(page), cfg as any).map(({ fix, ...r }) => ({ ...r, fix: fix?.label })) });
  }
}
const syncCases: [string, any, string][] = [
  ['arch/same', arch, archText],
  ['arch/edited', arch, edit(archText, (s) => s.replace('node cdn "CDN"', 'node cdn "Edge CDN"').replace(/^.*node q .*\n/m, '').replace('node mon', 'node newbie "New" icon=redis\nnode mon') + '\nnewbie -> lb "x"\n')],
  ['arch/moved', arch, archText.replace('node api2 "API 2" icon=service', 'node api2 "API 2" icon=service').replace(/group inner[^\n]*\n/, (m) => m).replace('node svc-a', 'group fresh "Fresh" {\n  node svc-a').replace('node svc-b "Ledger" icon=service', 'node svc-b "Ledger" icon=service\n}')],
  ['erd/same', erd, erdText],
  ['erd/edited', erd, erdText.replace('createdAt timestamptz', 'created_at timestamptz').replace('  qty int [not null]', '  qty int [not null]\n  price numeric(8, 2) [default: 0]')],
  ['erd/dropped', erd, erdText.replace(/^table audit_log \{[^}]*\}\n/m, '')],
  ['mixed/same', mixed, mixedText],
  ['uml/same', umlPage, umlText],
  ['uml/edited', umlPage, umlText.replace('+ run(): void', '+ run(): void\n    + stop(code: int): bool').replace('state st1 "Idle"', 'state st1 "Waiting"').replace(/^.*Inner" \{?\n?/m, (m) => m)],
  ['uml/dropped', umlPage, umlText.replace(/^  class Svc[^\n]*\n(?:    [^\n]*\n)*  \}\n/m, '').replace(/^  .* Svc .*\n/gm, '')],
  ['act/same', actPage, actText],
  ['act/edited', actPage, actText.replace('action a1 "Place order"', 'action a1 "Order it"').replace(/^.*decision d1.*\n/m, '').replace(/^.*d1.*\n/gm, '') + '\n'],
  ['act/lane', actPage, actText.replace('    action a1 "Place order"\n', '').replace('  note n1', '  action a1 "Place order"\n  note n1')],
  ['dep/same', depPage, depText],
  ['dep/edited', depPage, depText.replace('"HTTPS"', '"TLS"').replace('artifact app "app.apk"', 'artifact app "app.apk"\n    artifact extra "extra.bin"').replace('war manifests shop', 'war manifests shop\n  extra ..> phone')],
  ['dep/dropped', depPage, depText.replace(/^    component shop[^\n]*\n/m, '').replace(/^.* shop$/gm, '')],
  ['pkg/same', pkgPage, pkgText],
  ['pkg/edited', pkgPage, pkgText.replace('web imports domain', 'web access domain').replace(/^.*merges.*\n/m, '') + ''],
  ['pkg/dropped', pkgPage, pkgText.replace(/^packages \{[^}]*\}\n/m, '')],
  ['obj/same', objPage, objText],
  ['obj/edited', objPage, objText.replace('phone = 123', 'phone = 999').replace('tier = gold', 'tier = silver\n    extra = 1').replace(/^.*red.*\n/gm, '')],
  ['obj/dropped', objPage, objText.replace(/^ {2}object anon.*\n/m, '').replace(/^ {4}x = 1\n {2}\}\n/m, '')],
  ['comm/same', commPage, commText],
  ['comm/edited', commPage, commText.replace('1 alice -> bob "login()"', '1 alice -> bob "sign in()"').replace(/^ {2}3 .*\n/m, '') + ''],
  ['comm/dropped', commPage, commText.replace(/^ {2}2.* async\n/m, '').replace(/^ {2}2\.1.*\n/m, '')],
  ['timing/same', timingPage, timingText],
  ['timing/edited', timingPage, timingText.replace('wb @100 Waiting', 'wb @120 Waiting').replace('end 400', 'end 500').replace('  wb 100..300 "{200 ms}"\n', '  wb 120..300 "{180 ms}"\n  wu 0..100 "idle"\n')],
  ['timing/dropped', timingPage, timingText.replace(/^ {2}clock ck.*\n/m, '').replace(/^ {2}sw .*\n/gm, '').replace(/^ {2}binary .*\n/m, '')],
  ['uml/rename', umlPage, umlText.replace(/Svc/g, 'Service')],
  ['mixed/edited', mixed, mixedText.replace('web -> api: POST /order', 'web -> api: POST /orders\n  web -> db: peek')],
];
const seqLintText = `#!sdd 1
classes {
  class Cart {
    + add(id: int): void
  }
}
seq flow "Flow" {
  actor web
  participant Cart
  web -> Cart: add(1)
  web -> Cart: remove(1)
}
`;
const seqLintPage = applyDsl(undefined, seqLintText).page;
for (const [cfgName, cfg] of Object.entries({ none: {}, off: { rules: { 'uml/seq-missing-method': { enabled: false } } } })) {
  cases.lint.push({ name: `seqLint/${cfgName}`, page: seqLintPage, cfg, expected: lintPage(clone(seqLintPage), cfg as any).map(({ fix, ...r }) => ({ ...r, fix: fix?.label })) });
}
for (const [name, page, text] of syncCases) {
  for (const layout of [true, false]) {
    const r = applyDsl(clone(page), text, { layout });
    cases.dslApply.push({ name: `sync/${name}/${layout ? 'layout' : 'nolayout'}`, text, page, layout, expected: { page: r.page, diagnostics: r.diagnostics, added: r.added, removed: r.removed } });
  }
}

for (const [query, kind, limit] of [['', undefined, 5], ['redis', undefined, 10], ['load balancer', undefined, 20], ['aws s3', 'icon', 10], ['kafka cluster', 'cluster', 5], ['db', undefined, 30], ['postgres ha', undefined, 10], ['k8s', 'group', 10], ['api gateway', undefined, 15], ['zzzz', undefined, 5], ['  Message_Queue ', undefined, 8], ['vpc', 'group', 100], ['monitor', undefined, 7]] as [string, any, number][]) {
  cases.search.push({ name: `${query}|${kind}|${limit}`, query, kind, limit, expected: searchCatalog(query, { kind, limit }) });
}
for (const ref of ['redis', 'Redis', 'load balancer', 'LB', 'cluster-kafka', 'kafka', 'nope', 'aws group aws cloud', 'aws-group-aws-cloud', 'PostgreSQL']) cases.resolve.push({ ref, expected: resolveIcon(ref) ?? null });

writeFileSync(resolve(here, 'golden.json'), JSON.stringify(cases));
console.log(Object.entries(cases).map(([k, v]) => `${k}:${(v as any[]).length}`).join(' '));
