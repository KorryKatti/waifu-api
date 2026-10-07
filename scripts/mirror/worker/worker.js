/**
 * Cloudflare Worker: serves images from a private S3 bucket.
 *
 * Cloudflare fetches from the origin with SigV4 and caches at the edge, so the
 * bucket never needs public access. This is the way out of IDrive's
 * "no new public buckets" restriction, and it also removes origin egress as
 * a concern since each object is fetched roughly once.
 *
 * Deploy:
 *   npx wrangler deploy
 * Secrets (do NOT put these in wrangler.toml):
 *   npx wrangler secret put S3_ACCESS_KEY_ID
 *   npx wrangler secret put S3_SECRET_ACCESS_KEY
 */

const ALGO = "AWS4-HMAC-SHA256";
const SERVICE = "s3";

const enc = new TextEncoder();

async function hmac(key, msg) {
  const k = await crypto.subtle.importKey(
    "raw",
    key,
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"]
  );
  return new Uint8Array(await crypto.subtle.sign("HMAC", k, enc.encode(msg)));
}

function hex(buf) {
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

async function sha256Hex(text) {
  return hex(await crypto.subtle.digest("SHA-256", enc.encode(text)));
}

async function signingKey(secret, dateStamp, region) {
  const kDate = await hmac(enc.encode(`AWS4${secret}`), dateStamp);
  const kRegion = await hmac(kDate, region);
  const kService = await hmac(kRegion, SERVICE);
  return hmac(kService, "aws4_request");
}

/** Sign a GET for one object and return signed headers. */
async function signGet(host, path, region, accessKey, secretKey) {
  const now = new Date();
  const amzDate = now.toISOString().replace(/[:-]|\.\d{3}/g, "");
  const dateStamp = amzDate.slice(0, 8);

  // GET with no body.
  const payloadHash = await sha256Hex("");
  const signedHeaders = "host;x-amz-content-sha256;x-amz-date";
  const canonicalHeaders =
    `host:${host}\n` +
    `x-amz-content-sha256:${payloadHash}\n` +
    `x-amz-date:${amzDate}\n`;

  const canonicalRequest = [
    "GET",
    path,
    "",
    canonicalHeaders,
    signedHeaders,
    payloadHash,
  ].join("\n");

  const scope = `${dateStamp}/${region}/${SERVICE}/aws4_request`;
  const stringToSign = [
    ALGO,
    amzDate,
    scope,
    await sha256Hex(canonicalRequest),
  ].join("\n");

  const key = await signingKey(secretKey, dateStamp, region);
  const signature = hex(await hmac(key, stringToSign));

  return {
    "x-amz-date": amzDate,
    "x-amz-content-sha256": payloadHash,
    Authorization:
      `${ALGO} Credential=${accessKey}/${scope}, ` +
      `SignedHeaders=${signedHeaders}, Signature=${signature}`,
  };
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);

    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("Method Not Allowed", { status: 405 });
    }

    // Only plain GETs belong on this host; anything else goes to the API.
    if (url.search) {
      return Response.redirect(`https://${env.API_ORIGIN}${url.pathname}${url.search}`, 302);
    }

    // Reject traversal before it reaches the origin.
    const key = decodeURIComponent(url.pathname.replace(/^\/+/, ""));
    if (!key || key.includes("..") || key.includes("/")) {
      return new Response("Not Found", { status: 404 });
    }

    const host = new URL(env.S3_ENDPOINT).host;
    const path = `/${env.S3_BUCKET}/${key}`;

    const headers = await signGet(
      host,
      path,
      env.S3_REGION,
      env.S3_ACCESS_KEY_ID,
      env.S3_SECRET_ACCESS_KEY
    );

    // Serve from edge cache when warm; otherwise fetch and cache.
    const cache = caches.default;
    const cached = await cache.match(request);
    if (cached) {
      return cached;
    }

    const origin = await fetch(`https://${host}${path}`, { headers });

    if (!origin.ok) {
      return new Response("Not Found", { status: origin.status === 404 ? 404 : 502 });
    }

    const body = origin.body;
    const contentType = origin.headers.get("content-type") || "application/octet-stream";

    // Stream through so large images never buffer in memory.
    const response = new Response(request.method === "HEAD" ? null : body, {
      status: 200,
      headers: {
        "content-type": contentType,
        "cache-control": "public, max-age=31536000, immutable",
        "access-control-allow-origin": "*",
        etag: origin.headers.get("etag") || "",
      },
    });

    ctx.waitUntil(cache.put(request, response.clone()));
    return response;
  },
};