// node --check only parses. It does not catch a ReferenceError in a code path
// that never runs at check time, which is exactly how a deleted `useWebp`
// binding shipped and threw error 1101 on every image for fifteen minutes.
// This drives the real handler, so an unbound identifier fails the test.
//
// Run: node handler.test.mjs
import worker from './worker.js';

const MISSING = Object.assign(new Response('NoSuchKey', { status: 404 }), {});
const STORED = {
  '1.webp': 'PNGWEBP',      // the object a .png request should land on
  '6.webp': 'JPEGWEBP',     // ditto for .jpeg
  '9.jpeg': 'ORIGINALJPEG',
};

function stub({ originCalls }) {
  globalThis.caches = {
    default: { match: async () => undefined, put: async () => {} },
  };
  globalThis.fetch = async (url) => {
    const key = new URL(url).pathname.split('/').pop();
    originCalls.push(key);
    if (key in STORED) {
      return new Response(STORED[key], {
        status: 200, headers: { 'content-type': 'image/webp' },
      });
    }
    return MISSING;
  };
}

const env = {
  S3_ENDPOINT: 'https://t3.storage.dev',
  S3_BUCKET: 'waifu-api',
  WEBP_FALLBACK: 'true',
  WEBP_JPEG_FALLBACK: 'true',
};

let failures = 0;
async function check(label, path, wantKey, wantStatus = 200) {
  const originCalls = [];
  stub({ originCalls });
  let res;
  try {
    res = await worker.fetch(new Request('https://cdn.example' + path), env, {
      waitUntil() {},
    });
  } catch (e) {
    failures++;
    console.log(`  FAIL  ${label} threw ${e.constructor.name}: ${e.message}`);
    return;
  }
  const body = await res.text();
  if (res.status !== wantStatus) {
    failures++;
    console.log(`  FAIL  ${label} -> ${res.status} (want ${wantStatus})`);
    return;
  }
  if (wantStatus === 200 && wantKey && !originCalls.includes(wantKey)) {
    failures++;
    console.log(`  FAIL  ${label} fetched ${JSON.stringify(originCalls)}, want ${wantKey}`);
    return;
  }
  console.log(`  pass  ${label} -> ${res.status} body=${JSON.stringify(body.slice(0, 12))}`);
}

console.log('handler drives the real code path (catches unbound identifiers):');
await check('.png served from .webp',   '/1.png',  '1.webp');
await check('.jpeg served from .webp',  '/6.jpeg', '6.webp');
await check('fallback when no .webp',   '/9.jpeg', '9.jpeg');
await check('gif untouched',            '/x.gif',  'x.gif', 404);

console.log(failures ? `\n${failures} FAILED` : '\nall passed');
process.exit(failures ? 1 : 0);
