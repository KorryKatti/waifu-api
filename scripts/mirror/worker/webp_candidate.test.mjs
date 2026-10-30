// The extension arithmetic here decides whether a working image 404s. Silent
// failure, so it is tested rather than eyeballed.
import { webpCandidate } from './worker.js';

const PNG_ON = { WEBP_FALLBACK: 'true' };
const PNG_OFF = { WEBP_FALLBACK: 'false' };
const JPEG_ON = { WEBP_JPEG_FALLBACK: 'true' };
const JPEG_OFF = { WEBP_JPEG_FALLBACK: 'false' };

let failures = 0;
function check(label, got, want) {
  const ok = got === want;
  if (!ok) failures++;
  console.log(`  ${ok ? 'pass' : 'FAIL'}  ${label} -> ${JSON.stringify(got)}${ok ? '' : ` (want ${JSON.stringify(want)})`}`);
}

console.log('png path (deployed and serving traffic today):');
check('1.png',            webpCandidate('1.png', PNG_ON),     '1.webp');
check('1.PNG uppercase',  webpCandidate('1.PNG', PNG_ON),     '1.PNG'.slice(0,-4)+'.webp');
check('kill switch off',  webpCandidate('1.png', PNG_OFF),    null);
check('missing flag',     webpCandidate('1.png', {}),         null);

console.log('\njpeg path (new):');
check('6.jpeg',          webpCandidate('6.jpeg', JPEG_ON),   '6.webp');
check('6.jpg',           webpCandidate('6.jpg', JPEG_ON),    '6.webp');
check('6.JPEG',          webpCandidate('6.JPEG', JPEG_ON),   '6.JPEG'.slice(0,-5)+'.webp');
check('jpeg kill off',   webpCandidate('6.jpeg', JPEG_OFF),  null);
check('png flag does not affect jpeg',
                       webpCandidate('6.jpeg', PNG_ON),      null);

console.log('\nindependence — the reason there are two flags:');
check('png on, jpeg off', webpCandidate('1.png', { ...PNG_ON, ...JPEG_OFF }), '1.webp');
check('png off, jpeg on', webpCandidate('6.jpeg', { ...PNG_OFF, ...JPEG_ON }), '6.webp');

console.log('\nnever rewritten (a wrong answer here would lose the image):');
check('6.gif',            webpCandidate('6.gif', { ...PNG_ON, ...JPEG_ON }), null);
check('6.webp',          webpCandidate('6.webp', { ...PNG_ON, ...JPEG_ON }), null);
check('6.avif',          webpCandidate('6.avif', { ...PNG_ON, ...JPEG_ON }), null);
check('no extension',    webpCandidate('healthz', { ...PNG_ON, ...JPEG_ON }), null);

console.log(failures ? `\n${failures} FAILED` : '\nall passed');
process.exit(failures ? 1 : 0);
