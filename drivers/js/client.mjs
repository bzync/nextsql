// Shared session, streaming rows, and prepared statements for JS-family
// drivers. The runtime supplies a dial(cfg) → Wire function.

import {
  AuthPasswordKey,
  FlagCancel,
  HEADER,
  NextSQLError,
  ReadConsistency,
  Type,
  VERSION,
  decodeCommandComplete,
  decodeDataBatch,
  decodeError,
  decodeFrameHeader,
  decodeHelloOK,
  decodeNodeStatus,
  decodeRowDesc,
  encodeAuth,
  encodeExecute,
  encodeFrame,
  encodeHello,
  encodePrepare,
  encodeQuery,
  encodeSetReadConsistency,
  toBytes,
  u32,
} from './protocol.mjs';

export { ReadConsistency };

export class ByteReader {
  constructor() {
    this.chunks = [];
    this.len = 0;
    this.wait = null;
    this.err = null;
    this.closed = false;
  }

  push(chunk) {
    if (!chunk || chunk.length === 0) {
      return;
    }
    // Copy: runtimes reuse the socket buffer on the next read.
    const copy = new Uint8Array(chunk.length);
    copy.set(chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk));
    this.chunks.push(copy);
    this.len += copy.length;
    this.pump();
  }

  fail(err) {
    this.err = err instanceof NextSQLError ? err : new NextSQLError('io', err && err.message ? err.message : String(err));
    this.pump();
  }

  end() {
    this.closed = true;
    this.pump();
  }

  take(n) {
    const out = new Uint8Array(n);
    let off = 0;
    while (off < n) {
      const c = this.chunks[0];
      const need = n - off;
      if (c.length <= need) {
        out.set(c, off);
        off += c.length;
        this.chunks.shift();
      } else {
        out.set(c.subarray(0, need), off);
        this.chunks[0] = c.subarray(need);
        off += need;
      }
    }
    this.len -= n;
    return out;
  }

  pump() {
    if (!this.wait) {
      return;
    }
    if (this.err) {
      const { reject } = this.wait;
      this.wait = null;
      reject(this.err);
      return;
    }
    if (this.len >= this.wait.n) {
      const { resolve, n } = this.wait;
      this.wait = null;
      resolve(this.take(n));
      return;
    }
    if (this.closed) {
      const { reject } = this.wait;
      this.wait = null;
      reject(new NextSQLError('unavailable', 'connection closed'));
    }
  }

  readExact(n) {
    if (this.wait) {
      return Promise.reject(new NextSQLError('internal', 'overlapping read'));
    }
    return new Promise((resolve, reject) => {
      this.wait = { n, resolve, reject };
      this.pump();
    });
  }
}

export class Wire {
  constructor(reader, writeFn, closeFn) {
    this.reader = reader;
    this._write = writeFn;
    this._close = closeFn;
  }

  async readFrame() {
    const hdr = await this.reader.readExact(HEADER);
    const { type, length } = decodeFrameHeader(hdr);
    const payload = length === 0 ? new Uint8Array(0) : await this.reader.readExact(length);
    return { type, payload };
  }

  async writeFrame(typ, payload) {
    await this._write(encodeFrame(typ, payload));
  }

  close() {
    try {
      this._close();
    } catch {
      // already closed
    }
    this.reader.end();
  }
}

export class Rows {
  constructor(conn, columns) {
    this.conn = conn;
    this.columns = columns ? columns.map((c) => c.name) : [];
    this.columnTypes = columns || [];
    this.affected = 0;
    this._batch = [];
    this._i = 0;
    this._done = columns == null;
    this._closed = columns == null;
    this._err = null;
  }

  async next() {
    if (this._closed || this._err) {
      return false;
    }
    if (this._i < this._batch.length) {
      this._i++;
      return true;
    }
    if (this._done) {
      return false;
    }
    try {
      await this._fill();
    } catch (err) {
      this._err = err;
      return false;
    }
    if (this._i < this._batch.length) {
      this._i++;
      return true;
    }
    return false;
  }

  values() {
    if (this._i <= 0 || this._i > this._batch.length) {
      return null;
    }
    return this._batch[this._i - 1];
  }

  async _fill() {
    const c = this.conn;
    if (!this._done && this._batch.length > 0) {
      await c.wire.writeFrame(Type.FlowAck, null);
    }
    const msg = await c.wire.readFrame();
    if (msg.type === Type.DataBatch) {
      this._batch = decodeDataBatch(msg.payload);
      this._i = 0;
      return;
    }
    if (msg.type === Type.CommandComplete) {
      this.affected = decodeCommandComplete(msg.payload);
      this._done = true;
      this._batch = [];
      this._i = 0;
      await c.expectReady();
      this._finish();
      return;
    }
    throw c.unexpected(msg);
  }

  async close() {
    while (await this.next()) {
      // drain so the session returns to Ready
    }
    if (!this._closed) {
      this._finish();
    }
    if (this._err) {
      throw this._err;
    }
  }

  _finish() {
    if (this.conn && !this._closed) {
      this.conn.busy = false;
    }
    this._closed = true;
  }

  async *[Symbol.asyncIterator]() {
    try {
      while (await this.next()) {
        yield this.values();
      }
      if (this._err) {
        throw this._err;
      }
    } finally {
      if (!this._closed) {
        try {
          await this.close();
        } catch {
          // already surfaced via _err
        }
      }
    }
  }
}

export class Stmt {
  constructor(conn, id) {
    this.conn = conn;
    this.id = id;
  }

  async query(params) {
    return this.conn._execute(this.id, params || []);
  }

  async exec(params) {
    const rows = await this.query(params);
    return collect(rows);
  }

  async close() {
    const c = this.conn;
    if (!c.wire || !this.id) {
      return;
    }
    if (c.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await c.wire.writeFrame(Type.CloseStmt, putU32Local(this.id));
    const msg = await c.wire.readFrame();
    if (msg.type !== Type.CloseOK) {
      throw c.unexpected(msg);
    }
    await c.expectReady();
    this.id = 0;
  }
}

function putU32Local(n) {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setUint32(0, n >>> 0, true);
  return b;
}

export class Conn {
  constructor(cfg, wire, dial) {
    this.cfg = cfg;
    this.wire = wire;
    this.dial = dial;
    this.secret = 0n;
    this.busy = false;
  }

  async handshake() {
    await this.wire.writeFrame(Type.Hello, encodeHello({
      version: VERSION,
      database: this.cfg.database || '',
      user: this.cfg.user,
    }));
    let msg = await this.wire.readFrame();
    if (msg.type !== Type.HelloOK) {
      throw this.unexpected(msg);
    }
    const ok = decodeHelloOK(msg.payload);
    this.secret = ok.secret;
    await this.wire.writeFrame(Type.Auth, encodeAuth(this.cfg.password || ''));
    msg = await this.wire.readFrame();
    if (msg.type !== Type.AuthOK) {
      throw this.unexpected(msg);
    }
    if (ok.authMethod === AuthPasswordKey) {
      if (!this.cfg.key) {
        throw new NextSQLError('unauthorized', 'server requires a client-held key');
      }
      const key = toBytes(this.cfg.key);
      if (key.length !== 32) {
        throw new NextSQLError('invalid_argument', 'client key must be 32 bytes');
      }
      const mat = new Uint8Array(36);
      new DataView(mat.buffer).setUint32(0, this.cfg.keyVersion || 1, true);
      mat.set(key, 4);
      await this.wire.writeFrame(Type.Unlock, mat);
      msg = await this.wire.readFrame();
      if (msg.type !== Type.UnlockOK) {
        throw this.unexpected(msg);
      }
    }
    msg = await this.wire.readFrame();
    if (msg.type !== Type.Ready) {
      throw this.unexpected(msg);
    }
  }

  unexpected(msg) {
    if (msg.type === Type.Error) {
      return decodeError(msg.payload);
    }
    return new NextSQLError('protocol', 'unexpected message type');
  }

  async expectReady() {
    const msg = await this.wire.readFrame();
    if (msg.type !== Type.Ready) {
      throw this.unexpected(msg);
    }
  }

  // readAck reads a single control acknowledgement: Ready, or Error followed by
  // Ready (which is drained so the session stays usable).
  async readAck() {
    const msg = await this.wire.readFrame();
    if (msg.type === Type.Ready) {
      return;
    }
    const err = this.unexpected(msg);
    if (msg.type === Type.Error) {
      await this.expectReady();
    }
    throw err;
  }

  // setReadConsistency sets this connection's read-consistency mode for
  // subsequent statements. maxStalenessMs applies only to Bounded (0 or omitted
  // selects the server default window).
  async setReadConsistency(mode, maxStalenessMs) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await this.wire.writeFrame(Type.SetReadConsistency, encodeSetReadConsistency(mode, maxStalenessMs));
    await this.readAck();
  }

  // nodeStatus asks the connected server for its key-free replication health.
  async nodeStatus() {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await this.wire.writeFrame(Type.NodeStatus, null);
    const msg = await this.wire.readFrame();
    if (msg.type !== Type.NodeStatusResp) {
      const err = this.unexpected(msg);
      if (msg.type === Type.Error) {
        await this.expectReady();
      }
      throw err;
    }
    const st = decodeNodeStatus(msg.payload);
    await this.expectReady();
    return st;
  }

  async _readRows() {
    const msg = await this.wire.readFrame();
    if (msg.type === Type.RowDesc) {
      return new Rows(this, decodeRowDesc(msg.payload));
    }
    if (msg.type === Type.CommandComplete) {
      const rows = new Rows(this, null);
      rows.affected = decodeCommandComplete(msg.payload);
      await this.expectReady();
      this.busy = false;
      rows._closed = true;
      rows._done = true;
      return rows;
    }
    this.busy = false;
    throw this.unexpected(msg);
  }

  async query(sql, params) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    this.busy = true;
    try {
      await this.wire.writeFrame(Type.Query, encodeQuery(sql, params || []));
      return await this._readRows();
    } catch (err) {
      this.busy = false;
      throw err;
    }
  }

  async exec(sql, params) {
    const rows = await this.query(sql, params);
    return collect(rows);
  }

  async prepare(sql) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    await this.wire.writeFrame(Type.Prepare, encodePrepare(sql));
    const msg = await this.wire.readFrame();
    if (msg.type !== Type.PrepareOK) {
      throw this.unexpected(msg);
    }
    if (msg.payload.length !== 4) {
      throw new NextSQLError('protocol', 'bad prepare-ok length');
    }
    const id = u32(msg.payload, 0);
    await this.expectReady();
    return new Stmt(this, id);
  }

  async _execute(id, params) {
    if (!this.wire) {
      throw new NextSQLError('unavailable', 'connection closed');
    }
    if (this.busy) {
      throw new NextSQLError('conflict', 'connection is busy');
    }
    this.busy = true;
    try {
      await this.wire.writeFrame(Type.Execute, encodeExecute(id, params));
      return await this._readRows();
    } catch (err) {
      this.busy = false;
      throw err;
    }
  }

  async cancel() {
    if (!this.secret) {
      throw new NextSQLError('unavailable', 'not connected');
    }
    if (!this.dial) {
      throw new NextSQLError('unavailable', 'cancel requires a dialer');
    }
    const side = await this.dial(this.cfg);
    try {
      await side.writeFrame(Type.Hello, encodeHello({
        version: VERSION,
        flags: FlagCancel,
        secret: this.secret,
        database: '',
        user: '',
      }));
      const msg = await side.readFrame();
      if (msg.type !== Type.Ready) {
        throw this.unexpected(msg);
      }
    } finally {
      side.close();
    }
  }

  async close() {
    if (!this.wire) {
      return;
    }
    try {
      await this.wire.writeFrame(Type.Terminate, null);
    } catch {
      // ignore
    }
    this.wire.close();
    this.wire = null;
  }
}

export async function collect(rows) {
  const out = { columns: rows.columns, rows: [], affected: 0 };
  try {
    while (await rows.next()) {
      out.rows.push(rows.values());
    }
    if (rows._err) {
      throw rows._err;
    }
  } finally {
    if (!rows._closed) {
      await rows.close();
    }
  }
  out.affected = rows.affected;
  return out;
}

export async function open(cfg, dial) {
  const wire = await dial(cfg);
  const conn = new Conn(cfg, wire, dial);
  try {
    await conn.handshake();
    const mode = cfg.readConsistency;
    if (mode !== undefined && mode !== null && mode !== ReadConsistency.Strong) {
      await conn.setReadConsistency(mode, cfg.maxStalenessMs);
    }
  } catch (err) {
    wire.close();
    throw err;
  }
  return conn;
}

// statusTtlMs bounds how long a cached per-node NodeStatus is trusted before the
// Cluster re-probes it for a routing decision.
const statusTtlMs = 500;

// txnControl reports whether sql opens or closes an explicit transaction.
export function txnControl(sql) {
  const up = String(sql).replace(/^[\s(]+/, '').toUpperCase();
  const begin = up.startsWith('BEGIN') || up.startsWith('START TRANSACTION');
  const end = up.startsWith('COMMIT') || up.startsWith('ROLLBACK');
  return { begin, end };
}

// isReadOnlySQL is a conservative check: a false negative only costs a leader
// round trip, and a false positive on a write self-corrects (the follower
// rejects it as not-leader and the caller retries on the leader). EXPLAIN is
// excluded because EXPLAIN ANALYZE executes its statement.
export function isReadOnlySQL(sql) {
  let s = String(sql).replace(/^[\s(]+/, '');
  while (s.startsWith('--')) {
    const i = s.indexOf('\n');
    if (i < 0) {
      return false;
    }
    s = s.slice(i + 1).replace(/^[\s(]+/, '');
  }
  const up = s.toUpperCase();
  if (up.startsWith('SELECT') || up.startsWith('SHOW')) {
    return true;
  }
  if (up.startsWith('WITH')) {
    return !up.includes('INSERT') && !up.includes('UPDATE') &&
      !up.includes('DELETE') && !up.includes('UPSERT');
  }
  return false;
}

// Cluster is a routing client over every node of a NextSQL HA cluster.
//
// With cfg.readConsistency set to Bounded or Stale it sends eligible read-only
// statements to a healthy follower and everything else — writes, DDL,
// transaction control, and Strong reads — to the leader. With the default
// Strong consistency every statement goes to the leader and Cluster is just a
// leader-failover wrapper.
//
// A Cluster is for sequential use. Like Conn, an open Rows pins its connection
// until closed.
export class Cluster {
  constructor(cfg, conns) {
    this.cfg = cfg;
    this._conns = conns; // [{ addr, conn, status, seen }]
    this._rr = 0;
    this._inTxn = false;
  }

  async close() {
    let err = null;
    for (const cc of this._conns) {
      try {
        await cc.conn.close();
      } catch (e) {
        if (!err) {
          err = e;
        }
      }
    }
    if (err) {
      throw err;
    }
  }

  async nodes() {
    await this._refresh();
    return this._conns.map((cc) => cc.status).filter((s) => s != null);
  }

  async exec(sql, params) {
    const rows = await this.query(sql, params);
    return collect(rows);
  }

  async query(sql, params) {
    const { begin, end } = txnControl(sql);
    const routable = !this._inTxn && !begin && !end &&
      this.cfg.readConsistency !== undefined &&
      this.cfg.readConsistency !== null &&
      this.cfg.readConsistency !== ReadConsistency.Strong &&
      isReadOnlySQL(sql);

    if (routable) {
      const fc = await this._followerConn();
      if (fc) {
        try {
          return await fc.query(sql, params || []);
        } catch (err) {
          if (!(err instanceof NextSQLError) || err.code !== 'unavailable') {
            throw err;
          }
          // The follower lost the leader or fell outside the bound; the leader
          // can always answer, so fall through.
        }
      }
    }

    const lc = await this._leaderConn();
    const rows = await lc.query(sql, params || []);
    if (begin || end) {
      this._inTxn = begin;
    }
    return rows;
  }

  async _refresh() {
    const now = Date.now();
    const targets = this._conns.filter((cc) => now - (cc.seen || 0) >= statusTtlMs);
    for (const cc of targets) {
      try {
        cc.status = await cc.conn.nodeStatus();
        cc.seen = Date.now();
      } catch {
        // keep the last known status
      }
    }
  }

  async _leaderConn() {
    await this._refresh();
    for (const cc of this._conns) {
      const role = cc.status && cc.status.role;
      if (role === 'leader' || role === 'standalone') {
        return cc.conn;
      }
    }
    throw new NextSQLError('unavailable', 'no reachable leader');
  }

  async _followerConn() {
    await this._refresh();
    const followers = [];
    const others = [];
    for (const cc of this._conns) {
      if (!cc.status || !cc.status.healthy) {
        continue;
      }
      if (cc.status.role === 'follower') {
        followers.push(cc);
      } else if (cc.status.role === 'leader' || cc.status.role === 'standalone') {
        others.push(cc);
      }
    }
    const pick = followers.length > 0 ? followers : others;
    if (pick.length === 0) {
      return null;
    }
    const cc = pick[this._rr % pick.length];
    this._rr++;
    return cc.conn;
  }
}

// openCluster dials every address in cfg.nodes (or cfg.address when nodes is
// empty) and returns a routing client. It fails only when no node could be
// reached; a partially reachable cluster is usable.
export async function openCluster(cfg, dial) {
  let addrs = Array.isArray(cfg.nodes) ? cfg.nodes.slice() : [];
  if (addrs.length === 0 && cfg.address) {
    addrs = [cfg.address];
  }
  if (addrs.length === 0) {
    throw new NextSQLError('invalid_argument', 'at least one node address is required');
  }
  const conns = [];
  let firstErr = null;
  for (const a of addrs) {
    const nc = { ...cfg, address: a, nodes: undefined };
    try {
      const conn = await open(nc, dial);
      conns.push({ addr: a, conn, status: null, seen: 0 });
    } catch (err) {
      if (!firstErr) {
        firstErr = err;
      }
    }
  }
  if (conns.length === 0) {
    throw firstErr;
  }
  return new Cluster(cfg, conns);
}
