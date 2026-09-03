// Official NextSQL Bun driver. Speaks NSQL v1 over TLS 1.3.
// Encryption keys and passwords are never accepted in a URL.
// Transport uses Bun's node:net / node:tls (same framing as the Node driver).

import { X509Certificate } from 'node:crypto';
import { rename, stat, readFile, writeFile } from 'node:fs/promises';
import net from 'node:net';
import tls from 'node:tls';
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
// Bun: rotation and revocation persist across process restarts using the
// NSFK1 format (drivers/js/client-encryption.mjs). It uses node:fs/promises,
// which Bun implements natively. Production applications with an existing
// secret manager or KMS should implement the provider contract directly
// against that system instead.
class FileFieldKeyring {
  constructor(path, records) {
    this._path = path;
    this._records = records;
  }

  static async create(path, current) {
    try {
      await stat(path);
      throw new NextSQLError('already_exists', 'keyring file exists');
    } catch (err) {
      if (!(err && err.code === 'ENOENT')) throw err;
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
    const raw = await readFile(path);
    const records = decodeFieldKeyring(new Uint8Array(raw));
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
    const raw = await readFile(this._path);
    this._records = decodeFieldKeyring(new Uint8Array(raw));
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
    await writeFile(tmp, raw, { mode: 0o600 });
    await rename(tmp, this._path);
  }
}

function verifyPeer(sock, ca, servername) {
  if (ca === undefined || ca === null) {
    return;
  }
  const peer = sock.getPeerCertificate(true);
  if (!peer || !peer.raw) {
    throw new NextSQLError('io', 'missing peer certificate');
  }
  let trusted;
  try {
    trusted = new X509Certificate(ca);
  } catch (err) {
    throw new NextSQLError('io', err && err.message ? err.message : 'invalid CA certificate');
  }
  const presented = new X509Certificate(peer.raw);
  if (servername && !presented.checkHost(servername)) {
    throw new NextSQLError('io', 'hostname/IP does not match certificate');
  }
  if (presented.fingerprint256 === trusted.fingerprint256) {
    return;
  }
  if (!presented.verify(trusted.publicKey)) {
    throw new NextSQLError('io', 'unable to verify the first certificate');
  }
}

function connectSocket(cfg) {
  const { host, port } = splitHostPort(cfg.address);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    return Promise.reject(new NextSQLError('invalid_argument', 'invalid port'));
  }
  return new Promise((resolve, reject) => {
    const onErr = (err) => reject(new NextSQLError('io', err && err.message ? err.message : String(err)));
    if (cfg.tls) {
      const servername = cfg.tls.servername || host;
      const opts = {
        host,
        port,
        minVersion: 'TLSv1.3',
        servername,
      };
      if (cfg.tls.ca) {
        opts.ca = cfg.tls.ca;
        // Bun/BoringSSL rejects a self-signed leaf used as a trust
        // anchor. Handshake, then pin/verify the provided certificate.
        opts.rejectUnauthorized = false;
      }
      if (cfg.tls.rejectUnauthorized === false) {
        opts.rejectUnauthorized = false;
      }
      const sock = tls.connect(opts, () => {
        try {
          if (opts.rejectUnauthorized === false && cfg.tls.ca && cfg.tls.rejectUnauthorized !== false) {
            verifyPeer(sock, cfg.tls.ca, servername);
          }
        } catch (err) {
          sock.destroy();
          reject(err);
          return;
        }
        resolve(sock);
      });
      sock.on('error', onErr);
      return;
    }
    const sock = net.connect({ host, port }, () => resolve(sock));
    sock.on('error', onErr);
  });
}

export async function dial(cfg) {
  const sock = await connectSocket(cfg);
  const reader = new ByteReader();
  sock.on('data', (chunk) => reader.push(chunk));
  sock.on('error', (err) => reader.fail(err));
  sock.on('close', () => reader.end());
  return new Wire(
    reader,
    (bytes) => new Promise((resolve, reject) => {
      sock.write(Buffer.from(bytes), (err) => {
        if (err) {
          reject(new NextSQLError('io', err.message));
        } else {
          resolve();
        }
      });
    }),
    () => {
      sock.destroy();
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
