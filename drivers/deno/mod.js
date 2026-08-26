// Official NextSQL Deno driver. Speaks NSQL v1 over TLS 1.3.
// Encryption keys and passwords are never accepted in a URL.

import {
  Kind,
  NextSQLError,
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
import { ByteReader, Wire, open } from '../js/client.mjs';

export {
  Kind,
  NextSQLError,
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
  validateConfig,
};

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
