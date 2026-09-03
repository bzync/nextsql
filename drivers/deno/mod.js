// Official NextSQL Deno driver. Speaks NSQL v1 over TLS 1.3.
// Encryption keys and passwords are never accepted in a URL.

import {
  Kind,
  NextSQLError,
  ReadConsistency,
  Type,
  decodeDecimal,
  decodeHelloOK,
  decodeNSJB,
  decodeValue,
  encodeDecimalString,
  encodeHello,
  encodeParam,
  formatUUID,
  isLoopback,
  splitHostPort,
  validateConfig,
} from '../js/protocol.mjs';
import {
  ByteReader,
  Wire,
  isReadOnlySQL,
  open,
  openCluster,
  txnControl,
} from '../js/client.mjs';
import { decodeNodeStatus, encodeSetReadConsistency } from '../js/protocol.mjs';
import {
  FieldType,
  MemoryFieldKeyring,
  decodeFieldKeyring,
  decryptField,
  encodeFieldKeyring,
  encryptField,
  generateFieldKey,
  inspectField,
} from '../js/client-encryption.mjs';

export {
  Kind,
  NextSQLError,
  ReadConsistency,
  Type,
  decodeDecimal,
  decodeHelloOK,
  decodeNSJB,
  decodeNodeStatus,
  decodeValue,
  encodeDecimalString,
  encodeHello,
  encodeParam,
  encodeSetReadConsistency,
  formatUUID,
  isLoopback,
  isReadOnlySQL,
  txnControl,
  validateConfig,
  FieldType,
  MemoryFieldKeyring,
  FileFieldKeyring,
  decryptField,
  encryptField,
  generateFieldKey,
  inspectField,
};

// FileFieldKeyring is a durable, atomic, file-backed FieldKeyProvider for
// Deno: rotation and revocation persist across process restarts using the
// NSFK1 format (drivers/js/client-encryption.mjs). It uses Deno's native
// file API (matching the Deno.readTextFile convention already used for TLS
// CA loading in drivers/deno/live.js). Production applications with an
// existing secret manager or KMS should implement the provider contract
// directly against that system instead.
class FileFieldKeyring {
  constructor(path, records) {
    this._path = path;
    this._records = records;
  }

  static async create(path, current) {
    try {
      await Deno.stat(path);
      throw new NextSQLError('already_exists', 'keyring file exists');
    } catch (err) {
      if (!(err instanceof Deno.errors.NotFound)) throw err;
    }
    const material = current.material instanceof Uint8Array
      ? current.material
      : new Uint8Array(current.material || []);
    const record = {
      id: current.id,
      created: Math.floor(Date.now() / 1000),
      current: true,
      revoked: false,
      material,
    };
    const kr = new FileFieldKeyring(path, [record]);
    await kr._persist();
    return kr;
  }

  static async open(path) {
    const raw = await Deno.readFile(path);
    const records = decodeFieldKeyring(raw);
    return new FileFieldKeyring(path, records);
  }

  get path() {
    return this._path;
  }

  async currentFieldKey() {
    const rec = this._records.find((r) => r.current && !r.revoked);
    if (!rec) throw new NextSQLError('crypto', 'current field key unavailable');
    return { id: rec.id, material: rec.material.slice() };
  }

  async fieldKey(_database, _table, _column, id) {
    const rec = this._records.find((r) => r.id === id);
    if (!rec || rec.revoked) {
      throw new NextSQLError('crypto', 'field key unavailable or revoked');
    }
    return { id: rec.id, material: rec.material.slice() };
  }

  async rotate(key) {
    const material = key.material instanceof Uint8Array
      ? key.material
      : new Uint8Array(key.material || []);
    let rec = this._records.find((r) => r.id === key.id);
    if (rec && rec.revoked) {
      throw new NextSQLError('conflict', 'cannot reuse a revoked field key id');
    }
    if (!rec) {
      if (this._records.length >= 64) {
        throw new NextSQLError('exhausted', 'field key limit reached');
      }
      rec = { id: key.id, created: Math.floor(Date.now() / 1000), current: false, revoked: false, material };
      this._records.push(rec);
    }
    for (const r of this._records) r.current = false;
    rec.current = true;
    rec.material = material;
    await this._persist();
  }

  async revoke(id) {
    const rec = this._records.find((r) => r.id === id);
    if (!rec) throw new NextSQLError('not_found', 'unknown field key id');
    if (rec.current) {
      throw new NextSQLError('conflict', 'cannot revoke the current field key');
    }
    if (rec.revoked) return;
    rec.revoked = true;
    rec.material = new Uint8Array(32);
    await this._persist();
  }

  async reload() {
    const raw = await Deno.readFile(this._path);
    this._records = decodeFieldKeyring(raw);
  }

  list() {
    return this._records.map((r) => ({
      id: r.id,
      created: r.created,
      current: r.current,
      revoked: r.revoked,
    }));
  }

  async _persist() {
    const raw = encodeFieldKeyring(this._records);
    const tmp = `${this._path}.tmp`;
    await Deno.writeFile(tmp, raw, { mode: 0o600 });
    await Deno.rename(tmp, this._path);
  }
}

function caCerts(tls) {
  if (!tls) {
    return [];
  }
  if (Array.isArray(tls.caCerts)) {
    return tls.caCerts;
  }
  if (tls.ca === undefined || tls.ca === null) {
    return [];
  }
  if (typeof tls.ca === 'string') {
    return [tls.ca];
  }
  if (tls.ca instanceof Uint8Array) {
    return [new TextDecoder().decode(tls.ca)];
  }
  return [String(tls.ca)];
}

export async function dial(cfg) {
  const { host, port } = splitHostPort(cfg.address);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new NextSQLError('invalid_argument', 'invalid port');
  }
  let conn;
  try {
    const tcp = await Deno.connect({ hostname: host, port });
    if (cfg.tls) {
      conn = await Deno.startTls(tcp, {
        hostname: cfg.tls.servername || host,
        caCerts: caCerts(cfg.tls),
      });
    } else {
      conn = tcp;
    }
  } catch (err) {
    throw new NextSQLError('io', err && err.message ? err.message : String(err));
  }
  const reader = new ByteReader();
  (async () => {
    const buf = new Uint8Array(8192);
    try {
      while (true) {
        const n = await conn.read(buf);
        if (n === null) {
          reader.end();
          return;
        }
        reader.push(buf.slice(0, n));
      }
    } catch (err) {
      reader.fail(err);
    }
  })();
  return new Wire(
    reader,
    async (bytes) => {
      let off = 0;
      while (off < bytes.length) {
        const n = await conn.write(bytes.subarray(off));
        off += n;
      }
    },
    () => {
      try {
        conn.close();
      } catch {
        // already closed
      }
    },
  );
}

export async function connect(cfg) {
  validateConfig(cfg);
  return open(cfg, dial);
}

export async function connectCluster(cfg) {
  const addrs = Array.isArray(cfg && cfg.nodes) && cfg.nodes.length > 0
    ? cfg.nodes
    : [cfg && cfg.address];
  for (const a of addrs) {
    validateConfig({ ...cfg, address: a, nodes: undefined });
  }
  return openCluster(cfg, dial);
}
