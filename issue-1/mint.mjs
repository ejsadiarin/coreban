// Mint an EdDSA internal token like the gateway does: node mint.mjs <pem> <aud> <sub> <kid> [expired]
import { createPrivateKey, sign } from "node:crypto";
import { readFileSync } from "node:fs";
const [pem, aud, sub, kid, expired] = process.argv.slice(2);
const b64 = (o) => Buffer.from(JSON.stringify(o)).toString("base64url");
const now = Math.floor(Date.now() / 1000);
const header = { alg: "EdDSA", typ: "JWT", kid };
const claims = {
  iss: "https://gateway.internal", aud, sub, azp: "session", scope: "admin",
  iat: expired ? now - 600 : now, nbf: expired ? now - 600 : now,
  exp: expired ? now - 300 : now + 120,
};
const input = `${b64(header)}.${b64(claims)}`;
const sig = sign(null, Buffer.from(input), createPrivateKey(readFileSync(pem))).toString("base64url");
process.stdout.write(`${input}.${sig}`);
