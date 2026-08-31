// Official NextSQL Bun driver. Speaks NSQL v1 over TLS 1.3.
// Encryption keys and passwords are never accepted in a URL.
// Transport uses Bun's node:net / node:tls (same framing as the Node driver).

import { X509Certificate } from 'node:crypto';
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
};

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
