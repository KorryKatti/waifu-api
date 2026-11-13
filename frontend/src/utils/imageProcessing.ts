export interface OptimizeResult {
  file: File;
  converted: boolean;
}

const isGif = (file: File) => file.type === 'image/gif' || /\.gif$/i.test(file.name);

const withTimeout = <T,>(promise: Promise<T>, ms: number, label: string): Promise<T> =>
  new Promise<T>((resolve, reject) => {
    const t = window.setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms);
    promise.then(
      (v) => { window.clearTimeout(t); resolve(v); },
      (e) => { window.clearTimeout(t); reject(e); }
    );
  });

const loadImage = (file: File): Promise<HTMLImageElement> =>
  new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.decoding = 'async';
    img.onload = () => {
      URL.revokeObjectURL(url);
      resolve(img);
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error('Failed to decode image'));
    };
    img.src = url;
  });

export async function optimizeImageForUpload(file: File): Promise<OptimizeResult> {
  // Do not optimize animated images (GIFs). No resize for now.
  if (!file.type.startsWith('image/') || isGif(file)) {
    console.log('[upload] optimize: skip (gif/non-image)', file.name, file.type);
    return { file, converted: false };
  }

  try {
    console.log('[upload] optimize: start', file.name, file.size, file.type);
    const img = await withTimeout(loadImage(file), 20000, 'image decode');
    const width = img.naturalWidth || img.width;
    const height = img.naturalHeight || img.height;
    if (!width || !height) {
      console.warn('[upload] optimize: missing dimensions', file.name);
      return { file, converted: false };
    }

    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext('2d');
    if (!ctx) {
      console.warn('[upload] optimize: no 2d context', file.name);
      return { file, converted: false };
    }
    ctx.drawImage(img, 0, 0, width, height);

    const blob = await withTimeout(
      new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/webp', 0.92)),
      20000,
      'webp encode'
    );
    if (!blob || blob.size === 0) {
      console.warn('[upload] optimize: empty blob', file.name);
      return { file, converted: false };
    }

    const baseName = file.name.replace(/\.[^.]+$/, '') || 'upload';
    const out = new File([blob], `${baseName}.webp`, {
      type: 'image/webp',
      lastModified: Date.now(),
    });
    console.log('[upload] optimize: done', file.name, '->', out.name, out.size);
    return { file: out, converted: true };
  } catch (e) {
    console.warn('[upload] optimize: failed, using original', file.name, e);
    return { file, converted: false };
  }
}

export function createImageElement(file: File): Promise<HTMLImageElement> {
  console.log('[upload] nsfw: decode image element', file.name);
  return withTimeout(loadImage(file), 20000, 'image decode (nsfw)');
}
