import { useState, useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import api from '../services/api';
import SearchableSelect from '../components/SearchableSelect';
import { Info, Loader2, X, RotateCcw, Plus, ShieldAlert, ShieldCheck } from 'lucide-react';
import TagModal from '../components/modals/TagModal';
import ArtistModal from '../components/modals/ArtistModal';
import { useMetadata } from '../hooks/useMetadata';
import { ImageDto, Role } from '../types';
import { useRequireAuth } from '../hooks/useRequireAuth';
import { MetaTags } from '../hooks/useMetaTags';
import { getEnv } from '../utils/env';
import { optimizeImageForUpload, createImageElement } from '../utils/imageProcessing';
import { classifyImageElement } from '../utils/nsfw';

type FileStatus = 'queued' | 'uploading' | 'succeeded' | 'failed' | 'canceled';
type NsfwStatus = 'pending' | 'checking' | 'ready' | 'error' | 'skipped';

interface BatchFile {
  id: string;
  originalFile: File;
  uploadFile: File;
  converted: boolean;
  previewUrl: string;
  source: string;
  status: FileStatus;
  error?: string;
  imageId?: number;
  progress?: number;
  precheckError?: string;
  isNsfw: boolean;
  nsfwStatus: NsfwStatus;
  nsfwLabel?: string;
}

const isGifFile = (file: File) => file.type === 'image/gif' || /\.gif$/i.test(file.name);

const Upload = () => {
  const user = useRequireAuth();
  const navigate = useNavigate();

  const [files, setFiles] = useState<BatchFile[]>([]);
  const [isUploading, setIsUploading] = useState(false);
  const [agreedToTos, setAgreedToTos] = useState(false);
  const [nsfwAssistEnabled, setNsfwAssistEnabled] = useState(false);
  const [locked, setLocked] = useState(false);
  const [currentIndex, setCurrentIndex] = useState<number>(-1);
  const [liveAnnouncement, setLiveAnnouncement] = useState('');
  const cancelRef = useRef(false);
  const delayRef = useRef<number | null>(null);
  const filesRef = useRef<BatchFile[]>([]);
  filesRef.current = files;
  const nsfwAssistEnabledRef = useRef(nsfwAssistEnabled);
  nsfwAssistEnabledRef.current = nsfwAssistEnabled;

  // persist metadata draft (not files)
  useEffect(() => {
    try {
      const raw = localStorage.getItem('upload_batch_draft');
      if (!raw) return;
      const draft = JSON.parse(raw);
      if (typeof draft.agreedToTos === 'boolean') setAgreedToTos(draft.agreedToTos);
      if (typeof draft.nsfwAssistEnabled === 'boolean') setNsfwAssistEnabled(draft.nsfwAssistEnabled);
    } catch {}
  }, []);
  useEffect(() => {
    try {
      localStorage.setItem('upload_batch_draft', JSON.stringify({ agreedToTos, nsfwAssistEnabled }));
    } catch {}
  }, [agreedToTos, nsfwAssistEnabled]);

  const tosUrl = getEnv('VITE_TOS_URL');
  const isReviewMode = !(user?.role === Role.Moderator || user?.role === Role.Admin);

  useEffect(() => {
    if (nsfwAssistEnabled) {
      void runNsfwChecks();
    } else {
      // If turned off, mark non-final checks as skipped so they don't look stuck queued.
      setFiles(prev => prev.map(f =>
        (!f.precheckError && (f.nsfwStatus === 'pending' || f.nsfwStatus === 'checking'))
          ? { ...f, nsfwStatus: 'skipped', nsfwLabel: 'Opt-in to check' }
          : f
      ));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nsfwAssistEnabled]);

  const {
    selectedTags, setSelectedTags,
    selectedArtists, setSelectedArtists,
    showCreateTagModal, setShowCreateTagModal,
    newTagName, setNewTagName,
    showCreateArtistModal, setShowCreateArtistModal,
    newArtistName, setNewArtistName,
    loadTags, loadArtists,
    handleCreateTag, handleCreateArtist,
    slugify
  } = useMetadata({ isReviewMode });

  // Warn on close/refresh while uploading
  useEffect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (isUploading) {
        e.preventDefault();
        e.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [isUploading]);

  // Cleanup object URLs on unmount
  useEffect(() => {
    return () => {
      files.forEach(f => URL.revokeObjectURL(f.previewUrl));
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const updateFileById = (id: string, updater: (f: BatchFile) => BatchFile) => {
    setFiles(prev => prev.map(f => (f.id === id ? updater(f) : f)));
  };

  const classifyFileAsync = async (id: string, originalFile: File) => {
    if (!originalFile.type.startsWith('image/')) {
      updateFileById(id, f => ({ ...f, nsfwStatus: 'skipped', nsfwLabel: 'Not an image' }));
      return;
    }
    console.log('[upload] nsfw: start', id, originalFile.name);
    updateFileById(id, f => ({ ...f, nsfwStatus: 'checking', nsfwLabel: undefined }));
    try {
      const img = await createImageElement(originalFile);
      const assessment = await classifyImageElement(img);
      const label = `${assessment.topClass} ${Math.round(assessment.topProbability * 100)}%`;
      console.log('[upload] nsfw: done', id, label, 'isNsfw=', assessment.isNsfw);
      updateFileById(id, f => ({
        ...f,
        // Auto-mark when suggested; user can override per file.
        isNsfw: assessment.isNsfw ? true : f.isNsfw,
        nsfwStatus: 'ready',
        nsfwLabel: label,
      }));
    } catch (e) {
      console.warn('[upload] nsfw: failed', id, originalFile.name, e);
      updateFileById(id, f => ({ ...f, nsfwStatus: 'error', nsfwLabel: 'Check unavailable' }));
    }
  };

  const prepareFileAsync = async (id: string, originalFile: File, shouldClassify: boolean) => {
    console.log('[upload] prepare: start', id, originalFile.name, { shouldClassify });
    try {
      // Convert static images to WebP (no resize). Animated GIFs left as-is.
      const optimized = await optimizeImageForUpload(originalFile);
      updateFileById(id, f => ({ ...f, uploadFile: optimized.file, converted: optimized.converted }));
      console.log('[upload] prepare: optimized', id, { converted: optimized.converted, size: optimized.file.size, type: optimized.file.type });

      if (shouldClassify) {
        await classifyFileAsync(id, originalFile);
      } else {
        updateFileById(id, f =>
          f.nsfwStatus === 'pending' || f.nsfwStatus === 'checking'
            ? { ...f, nsfwStatus: 'skipped', nsfwLabel: 'Opt-in to check' }
            : f
        );
        console.log('[upload] prepare: skipped nsfw (opt-in off)', id);
      }
      console.log('[upload] prepare: done', id);
    } catch (e) {
      console.error('[upload] prepare: unexpected error', id, originalFile.name, e);
      // Ensure items never remain stuck in "queued/pending" states due to unexpected errors.
      updateFileById(id, f => ({
        ...f,
        nsfwStatus: f.nsfwStatus === 'checking' || f.nsfwStatus === 'ready' ? f.nsfwStatus : (shouldClassify ? 'error' : 'skipped'),
        nsfwLabel: f.nsfwLabel || (shouldClassify ? 'Check unavailable' : 'Opt-in to check'),
      }));
    }
  };

  const addFiles = (fileList: FileList | null) => {
    if (!fileList || locked || isUploading) return;
    const incoming = Array.from(fileList);
    const prev = filesRef.current;
    const toAdd: BatchFile[] = [];
    const toPrepare: { id: string; file: File }[] = [];
    const maxSizeMb = 10; // conservative client hint; backend enforces actual limit

    for (const file of incoming) {
      if (prev.length + toAdd.length >= 10) break;
      if (
        prev.some(p => p.originalFile.name === file.name && p.originalFile.size === file.size) ||
        toAdd.some(p => p.originalFile.name === file.name && p.originalFile.size === file.size)
      ) {
        continue;
      }
      let precheckError: string | undefined;
      if (!file.type.startsWith('image/')) precheckError = 'Not an image file.';
      else if (file.size <= 0) precheckError = 'File is empty.';
      else if (file.size > maxSizeMb * 1024 * 1024) precheckError = `File is too large (max ~${maxSizeMb}MB client hint).`;

      const id = `${file.name}-${file.size}-${Date.now()}-${Math.random()}`;
      const item: BatchFile = {
        id,
        originalFile: file,
        uploadFile: file,
        converted: false,
        previewUrl: URL.createObjectURL(file),
        source: '',
        status: precheckError ? 'failed' : 'queued',
        progress: 0,
        precheckError,
        error: precheckError,
        isNsfw: false,
        nsfwStatus: precheckError ? 'skipped' : 'pending',
      };
      toAdd.push(item);
      if (!precheckError) toPrepare.push({ id, file });
    }

    if (toAdd.length === 0) return;
    setFiles([...prev, ...toAdd].slice(0, 10));

    const shouldClassify = nsfwAssistEnabledRef.current;
    console.log('[upload] addFiles: queued for prep', toPrepare.map(t => ({ id: t.id, name: t.file.name, type: t.file.type, size: t.file.size })), { nsfwAssistEnabled: shouldClassify });
    // Process sequentially to avoid CPU/memory spikes (conversion + optional NSFW model inference).
    void (async () => {
      for (const item of toPrepare) {
        await prepareFileAsync(item.id, item.file, shouldClassify);
      }
      console.log('[upload] addFiles: prep queue finished');
    })();
  };

  const removeFile = (id: string) => {
    if (locked || isUploading) return;
    setFiles(prev => {
      const toRemove = prev.find(f => f.id === id);
      if (toRemove) URL.revokeObjectURL(toRemove.previewUrl);
      return prev.filter(f => f.id !== id);
    });
  };

  const nsfwRunRef = useRef<Promise<void> | null>(null);

  const runNsfwChecks = async () => {
    if (nsfwRunRef.current) return nsfwRunRef.current;
    const run = (async () => {
      try {
        // Loop so files added/marked while running are picked up.
        for (;;) {
          if (!nsfwAssistEnabledRef.current) break;
          const targets = filesRef.current.filter(
            f => !f.precheckError && f.originalFile.type.startsWith('image/') && (f.nsfwStatus === 'pending' || f.nsfwStatus === 'skipped' || f.nsfwStatus === 'error')
          );
          if (targets.length === 0) break;
          let progressed = false;
          for (const item of targets) {
            if (!nsfwAssistEnabledRef.current) break;
            const latest = filesRef.current.find(x => x.id === item.id);
            if (!latest) continue;
            if (latest.nsfwStatus === 'checking' || latest.nsfwStatus === 'ready') continue;
            await classifyFileAsync(item.id, item.originalFile);
            progressed = true;
          }
          if (!progressed) break;
        }
      } finally {
        nsfwRunRef.current = null;
      }
    })();
    nsfwRunRef.current = run;
    return run;
  };

  const updateSource = (id: string, source: string) => {
    if (locked) return;
    setFiles(prev => prev.map(f => f.id === id ? { ...f, source } : f));
  };

  const toggleFileNsfw = (id: string) => {
    if (locked || isUploading) return;
    setFiles(prev => prev.map(f => f.id === id ? { ...f, isNsfw: !f.isNsfw } : f));
  };

  const getBackoffDelay = (error: any): number => {
    try {
      const resp = error?.response;
      if (resp) {
        const status = resp.status;
        if (status === 429 || status === 503) {
          const headers = resp.headers || {};
          const retryAfter = headers['retry-after'] || headers['Retry-After'];
          if (retryAfter) {
            const seconds = parseInt(String(retryAfter), 10);
            if (!isNaN(seconds) && seconds > 0) {
              return Math.min(seconds * 1000, 30000);
            }
          }
          return status === 503 ? 5000 : 10000;
        }
        if (status === 409) {
          return 1000;
        }
      }
      if (error?.code === 'ECONNABORTED' || error?.message?.toLowerCase().includes('timeout')) {
        return 5000;
      }
    } catch {}
    return 2000;
  };

  const formatUploadError = (error: any): string => {
    const resp = error?.response;
    if (resp) {
      if (resp.status === 409) return 'Duplicate image found or conflict (may have already been uploaded).';
      if (resp.status === 503) return error?.message || 'Server busy. Will retry with backoff.';
      if (resp.status === 429) return error?.message || 'Rate limited. Will retry with backoff.';
      if (resp.status === 413) return error?.message || 'File too large.';
    }
    return error?.message || 'Upload failed';
  };

  const buildFormData = (item: BatchFile) => {
    const formData = new FormData();
    formData.append('file', item.uploadFile);
    selectedTags.forEach(tag => formData.append('tags', tag.slug || slugify(tag.name.trim())));
    selectedArtists.forEach(artist => formData.append('artists', String(artist.id)));
    if (item.source && item.source.trim()) {
      formData.append('source', item.source.trim());
    }
    formData.append('isNsfw', String(item.isNsfw));
    return formData;
  };

  const uploadBatch = async () => {
    if (files.length === 0) return;
    if (tosUrl && !agreedToTos) return;
    const invalid = files.find(f => f.source && f.source.trim() && !/^https?:\/\//i.test(f.source.trim()));
    if (invalid) {
      setLiveAnnouncement(`Invalid source URL for ${invalid.originalFile.name}. Please fix before starting.`);
      return;
    }

    setIsUploading(true);
    setLocked(true);
    cancelRef.current = false;

    const snapshot = files.map(f => ({ ...f }));
    setFiles(snapshot);

    for (let i = 0; i < snapshot.length; i++) {
      if (cancelRef.current) {
        setFiles(prev => prev.map((f, idx) => idx >= i && f.status === 'queued' ? { ...f, status: 'canceled' } : f));
        setLiveAnnouncement('Batch canceled; remaining items marked canceled.');
        break;
      }
      if (snapshot[i].status === 'failed' && snapshot[i].precheckError) {
        continue;
      }
      setCurrentIndex(i);
      setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, status: 'uploading', error: undefined } : f));
      setLiveAnnouncement(`Uploading ${i + 1} of ${snapshot.length}: ${snapshot[i].originalFile.name}`);

      const item = snapshot[i];
      try {
        const { data: uploadedImage } = await api.post<ImageDto>('/images/upload', buildFormData(item), {
          headers: { 'Content-Type': 'multipart/form-data' },
          onUploadProgress: (evt) => {
            if (!evt.total) return;
            const pct = Math.round((evt.loaded / evt.total) * 100);
            setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, progress: pct } : f));
          }
        });
        setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, status: 'succeeded', imageId: uploadedImage.id, progress: 100 } : f));
        setLiveAnnouncement(`Uploaded ${snapshot[i].originalFile.name} successfully.`);
      } catch (e: any) {
        const msg = formatUploadError(e);
        setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, status: 'failed', error: msg, progress: 0 } : f));
        setLiveAnnouncement(`Failed to upload ${snapshot[i].originalFile.name}: ${msg}`);
        if (i < snapshot.length - 1 && !cancelRef.current) {
          const delay = getBackoffDelay(e);
          await new Promise(resolve => { delayRef.current = window.setTimeout(resolve, delay); });
          if (cancelRef.current) break;
          continue;
        }
      }

      if (i < snapshot.length - 1 && !cancelRef.current) {
        await new Promise(resolve => {
          delayRef.current = window.setTimeout(resolve, 2000);
        });
      }
    }

    setIsUploading(false);
    setCurrentIndex(-1);
    if (delayRef.current) {
      clearTimeout(delayRef.current);
      delayRef.current = null;
    }
    setLiveAnnouncement('Batch upload finished.');
  };

  const cancelRemaining = () => {
    if (!isUploading) return;
    cancelRef.current = true;
    if (delayRef.current) {
      clearTimeout(delayRef.current);
      delayRef.current = null;
    }
  };

  const retryFailed = async () => {
    if (isUploading) return;
    const failedIndices = files.map((f, idx) => ({ f, idx })).filter(x => x.f.status === 'failed');
    if (failedIndices.length === 0) return;
    setIsUploading(true);
    cancelRef.current = false;
    const snapshot = files.map(f => ({ ...f }));
    for (let j = 0; j < failedIndices.length; j++) {
      if (cancelRef.current) break;
      const { idx } = failedIndices[j];
      setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'uploading', error: undefined, progress: 0 } : f));
      setLiveAnnouncement(`Retrying ${snapshot[idx].originalFile.name}`);
      try {
        const { data: uploadedImage } = await api.post<ImageDto>('/images/upload', buildFormData(snapshot[idx]), {
          headers: { 'Content-Type': 'multipart/form-data' },
          onUploadProgress: (evt) => {
            if (!evt.total) return;
            const pct = Math.round((evt.loaded / evt.total) * 100);
            setFiles(prev => prev.map((f, i) => i === idx ? { ...f, progress: pct } : f));
          }
        });
        setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'succeeded', imageId: uploadedImage.id, progress: 100 } : f));
      } catch (e: any) {
        const msg = formatUploadError(e);
        setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'failed', error: msg, progress: 0 } : f));
        if (j < failedIndices.length - 1 && !cancelRef.current) {
          const delay = getBackoffDelay(e);
          await new Promise(resolve => { delayRef.current = window.setTimeout(resolve, delay); });
          if (cancelRef.current) break;
          continue;
        }
      }
      if (j < failedIndices.length - 1 && !cancelRef.current) {
        await new Promise(resolve => { delayRef.current = window.setTimeout(resolve, 2000); });
      }
    }
    setIsUploading(false);
    if (delayRef.current) { clearTimeout(delayRef.current); delayRef.current = null; }
  };

  const retrySingle = async (id: string) => {
    if (isUploading) return;
    const idx = files.findIndex(f => f.id === id && f.status === 'failed');
    if (idx === -1) return;
    const item = files[idx];
    setIsUploading(true);
    cancelRef.current = false;
    setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'uploading', error: undefined, progress: 0 } : f));
    setLiveAnnouncement(`Retrying ${item.originalFile.name}`);
    try {
      const { data: uploadedImage } = await api.post<ImageDto>('/images/upload', buildFormData(item), {
        headers: { 'Content-Type': 'multipart/form-data' },
        onUploadProgress: (evt) => {
          if (!evt.total) return;
          const pct = Math.round((evt.loaded / evt.total) * 100);
          setFiles(prev => prev.map((f, i) => i === idx ? { ...f, progress: pct } : f));
        }
      });
      setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'succeeded', imageId: uploadedImage.id, progress: 100 } : f));
    } catch (e: any) {
      const msg = formatUploadError(e);
      setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'failed', error: msg, progress: 0 } : f));
    }
    setIsUploading(false);
  };

  const succeededCount = files.filter(f => f.status === 'succeeded').length;
  const failedCount = files.filter(f => f.status === 'failed').length;
  const canceledCount = files.filter(f => f.status === 'canceled').length;
  const done = !isUploading && locked && files.length > 0 && (succeededCount + failedCount + canceledCount === files.length);

  if (!user) return null;

  const liveStatus = liveAnnouncement || (isUploading
    ? `Uploading ${Math.max(0, currentIndex + 1)} of ${files.length}`
    : done
      ? `Batch complete. ${succeededCount} succeeded, ${failedCount} failed, ${canceledCount} canceled.`
      : '');

  return (
    <div className="max-w-6xl mx-auto p-4 md:p-8">
      <MetaTags
        title="Upload"
        description="Submit images in batches (up to 10). Static images are converted to WebP in your browser before upload; optional on-device NSFW suggestions are available."
        noindex
      />
      <div className="sr-only" aria-live="polite" role="status">{liveStatus}</div>
      <div className="bg-blue-500/10 border border-blue-500/20 rounded-xl p-4 mb-8 flex items-start gap-3">
        <Info className="text-blue-500 shrink-0 mt-0.5" size={20} />
        <div>
          <h4 className="font-bold text-blue-600 dark:text-blue-400 text-sm">Review Process</h4>
          <p className="text-sm text-muted-foreground mt-1">
            New uploads require review approval before appearing publicly. Batch uploads are sent one-by-one with a short delay.
          </p>
          <p className="text-sm text-muted-foreground mt-1">
            Static images are converted to WebP in your browser before upload (no resize). Animated GIFs are uploaded as-is. You can opt in to on-device NSFW suggestions per file (model downloads in-browser on first use; Sexy &gt; 69% is marked NSFW); you can always toggle NSFW per image.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        <div className="order-2 lg:order-1 space-y-4">
          <div
            className={`flex flex-col items-center justify-center w-full h-[200px] border-2 border-dashed rounded-2xl transition-all duration-300 relative overflow-hidden ${locked ? 'opacity-60 cursor-not-allowed' : 'cursor-pointer hover:bg-secondary/50 hover:border-primary/50 border-border bg-secondary/30'}`}
            onDragOver={(e) => { if (!locked && !isUploading) e.preventDefault(); }}
            onDrop={(e) => {
              if (locked || isUploading) return;
              e.preventDefault();
              addFiles(e.dataTransfer.files);
            }}
          >
            <input
              type="file"
              accept="image/*"
              multiple
              disabled={locked || isUploading}
              onChange={(e) => addFiles(e.target.files)}
              className="absolute inset-0 opacity-0 cursor-pointer disabled:cursor-not-allowed"
              aria-label="Add images to batch"
            />
            <div className="text-center p-6 text-muted-foreground pointer-events-none">
              <Plus size={28} className="mx-auto mb-2" />
              <p className="text-lg font-medium text-foreground">Add images (up to 10)</p>
              <p className="text-sm mt-1">Click or drag images here</p>
              <p className="text-xs mt-1">{files.length}/10 selected</p>
            </div>
          </div>

          <div className="space-y-2 max-h-[520px] overflow-auto">
            {files.map((f, idx) => (
              <div key={f.id} className="flex items-start gap-3 p-3 border border-border rounded-xl bg-card">
                <img src={f.previewUrl} alt="preview" className="w-16 h-16 object-contain rounded-lg bg-secondary mt-0.5" />
                <div className="flex-1 min-w-0 space-y-1.5">
                  <div className="flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <p className="text-sm font-medium truncate" title={f.originalFile.name}>{f.originalFile.name}</p>
                      <p className="text-xs text-muted-foreground">
                        {(f.originalFile.size / 1024).toFixed(1)} KB
                        {f.converted && <span className="ml-2 text-emerald-600 dark:text-emerald-400">→ WebP {(f.uploadFile.size / 1024).toFixed(1)} KB</span>}
                        {!f.converted && isGifFile(f.originalFile) && <span className="ml-2">GIF (not converted)</span>}
                      </p>
                    </div>
                    <span className={`text-xs px-2 py-0.5 rounded-full whitespace-nowrap ${f.status === 'succeeded' ? 'bg-green-500/10 text-green-600' : f.status === 'failed' ? 'bg-red-500/10 text-red-600' : f.status === 'uploading' ? 'bg-blue-500/10 text-blue-600' : f.status === 'canceled' ? 'bg-yellow-500/10 text-yellow-600' : 'bg-secondary text-muted-foreground'}`}>
                      {f.status}{idx === currentIndex && isUploading ? ` ${f.progress ?? 0}%` : ''}
                    </span>
                  </div>

                  <div className="flex items-center gap-2 flex-wrap">
                    {f.isNsfw ? (
                      <span className="inline-flex items-center gap-1 text-xs font-semibold px-2 py-0.5 rounded bg-red-500/15 text-red-600 dark:text-red-400">
                        <ShieldAlert size={12} /> NSFW
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-green-500/10 text-green-700 dark:text-green-400">
                        <ShieldCheck size={12} /> SFW
                      </span>
                    )}
                    <span className="text-xs text-muted-foreground">
                      {f.nsfwStatus === 'checking' && 'Checking NSFW…'}
                      {f.nsfwStatus === 'pending' && 'NSFW check queued…'}
                      {f.nsfwStatus === 'ready' && f.nsfwLabel}
                      {f.nsfwStatus === 'error' && (f.nsfwLabel || 'NSFW check unavailable')}
                      {f.nsfwStatus === 'skipped' && 'NSFW check skipped'}
                    </span>
                    {!locked && !isUploading && (
                      <button
                        type="button"
                        onClick={() => toggleFileNsfw(f.id)}
                        className="text-xs px-2 py-0.5 rounded border border-border hover:bg-secondary transition-colors"
                        aria-label={`Toggle NSFW for ${f.originalFile.name}`}
                      >
                        Mark as {f.isNsfw ? 'SFW' : 'NSFW'}
                      </button>
                    )}
                  </div>

                  {f.status === 'uploading' && (
                    <div className="w-full h-1.5 bg-secondary rounded overflow-hidden" aria-label={`Upload progress for ${f.originalFile.name}`}>
                      <div className="h-full bg-primary transition-all" style={{ width: `${f.progress ?? 0}%` }} />
                    </div>
                  )}
                  <input
                    type="url"
                    value={f.source}
                    disabled={locked}
                    onChange={(e) => updateSource(f.id, e.target.value)}
                    placeholder="Source URL (per image, optional)"
                    className="w-full text-xs p-2 rounded-lg bg-secondary border-transparent focus:ring-1 focus:ring-primary outline-none"
                    aria-label={`Source URL for ${f.originalFile.name}`}
                  />
                  {f.error && <p className="text-xs text-destructive" title={f.error}>{f.error}</p>}
                  {f.imageId && <p className="text-xs text-muted-foreground">ID: {f.imageId}</p>}
                  {f.status === 'failed' && !isUploading && !f.precheckError && (
                    <button onClick={() => retrySingle(f.id)} className="text-xs text-primary hover:underline mt-1 inline-flex items-center gap-1" aria-label={`Retry upload for ${f.originalFile.name}`}>
                      <RotateCcw size={12} /> Retry
                    </button>
                  )}
                </div>
                {!locked && (
                  <button onClick={() => removeFile(f.id)} className="p-2 hover:bg-secondary rounded-lg" aria-label={`Remove ${f.originalFile.name}`}>
                    <X size={16} />
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>

        <div className="order-1 lg:order-2 space-y-6">
          <h1 className="text-3xl font-black mb-2">Batch Upload</h1>
          <div className="space-y-5">
            <div className="space-y-4">
              <SearchableSelect
                label="Artists (shared)"
                placeholder="Search artists..."
                loadOptions={loadArtists}
                selectedOptions={selectedArtists}
                onSelect={(o) => setSelectedArtists(p => p.some(a => a.id === o.id) ? p : [...p, o])}
                onRemove={(o) => setSelectedArtists(p => p.filter(a => a.id !== o.id))}
                onCreate={(name) => { setNewArtistName(name.trim()); setShowCreateArtistModal(true); }}
                isMulti={true}
              />
              <SearchableSelect
                label="Tags (shared)"
                placeholder="Add tags..."
                loadOptions={loadTags}
                selectedOptions={selectedTags}
                onSelect={(o) => setSelectedTags(p => p.some(t => t.id === o.id) ? p : [...p, o])}
                onRemove={(o) => setSelectedTags(p => p.filter(t => t.id !== o.id))}
                onCreate={(name) => { setNewTagName(name.trim()); setShowCreateTagModal(true); }}
                isMulti={true}
              />
              <div className="p-4 border border-border rounded-xl bg-secondary/30 text-sm text-muted-foreground space-y-3">
                <label className="flex items-start gap-3 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={nsfwAssistEnabled}
                    onChange={(e) => setNsfwAssistEnabled(e.target.checked)}
                    disabled={locked || isUploading}
                    className="w-5 h-5 rounded text-primary focus:ring-primary mt-0.5"
                  />
                  <span>
                    <span className="block font-medium text-foreground">Suggest NSFW with on-device model (opt-in)</span>
                    <span className="block mt-0.5">Runs locally in your browser and downloads a model on first use. Shows a per-file suggestion (Sexy &gt; 69% marked NSFW); you can override it.</span>
                  </span>
                </label>
                <p>NSFW is set per file and sent with each upload. You can toggle it on any card before uploading.</p>
              </div>
            </div>

            {tosUrl && (
              <label className="flex items-start gap-3 p-4 border border-border rounded-xl cursor-pointer hover:bg-secondary/50 transition-colors">
                <input
                  type="checkbox"
                  checked={agreedToTos}
                  onChange={e => setAgreedToTos(e.target.checked)}
                  disabled={locked}
                  className="w-5 h-5 rounded text-primary focus:ring-primary mt-0.5"
                />
                <span className="text-sm text-muted-foreground">
                  By uploading, I agree to the{' '}
                  <a href={tosUrl} target="_blank" rel="noopener noreferrer" className="text-primary hover:underline font-medium">
                    Terms of Service
                  </a>
                </span>
              </label>
            )}

            <div className="flex flex-col gap-2">
              <button
                onClick={uploadBatch}
                disabled={isUploading || files.length === 0 || (!!tosUrl && !agreedToTos)}
                className="w-full py-4 rounded-xl bg-foreground text-background font-bold text-lg hover:opacity-90 transition-all shadow-lg flex items-center justify-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {isUploading ? (
                  <>
                    <Loader2 size={20} className="animate-spin" />
                    Uploading {currentIndex + 1}/{files.length}...
                  </>
                ) : (
                  `Upload ${files.length} image${files.length === 1 ? '' : 's'}`
                )}
              </button>
              {isUploading && (
                <button onClick={cancelRemaining} className="w-full py-2 rounded-xl border border-border font-medium hover:bg-secondary transition-all">
                  Cancel remaining
                </button>
              )}
              {!isUploading && locked && failedCount > 0 && (
                <button onClick={retryFailed} className="w-full py-2 rounded-xl border border-border font-medium hover:bg-secondary transition-all flex items-center justify-center gap-2">
                  <RotateCcw size={16} /> Retry all failed ({failedCount})
                </button>
              )}
            </div>

            {done && (
              <div className="p-4 border border-border rounded-xl bg-card space-y-2">
                <p className="font-bold">Batch complete</p>
                <p className="text-sm text-muted-foreground">Succeeded: {succeededCount}, Failed: {failedCount}, Canceled: {canceledCount}</p>
                {isReviewMode && succeededCount > 0 && (
                  <button onClick={() => navigate('/review?type=Image')} className="text-primary hover:underline text-sm font-medium">Go to review queue</button>
                )}
                {!isReviewMode && succeededCount > 0 && (
                  <p className="text-sm text-muted-foreground">Check your uploads in the gallery/review as applicable.</p>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      <TagModal
        isOpen={showCreateTagModal}
        onClose={() => setShowCreateTagModal(false)}
        onSubmit={handleCreateTag}
        initialData={{ name: newTagName }}
        title="New Tag"
        isReviewMode={isReviewMode}
        submitLabel="Create"
      />

      <ArtistModal
        isOpen={showCreateArtistModal}
        onClose={() => setShowCreateArtistModal(false)}
        onSubmit={handleCreateArtist}
        initialData={{ name: newArtistName }}
        title="New Artist"
        isReviewMode={isReviewMode}
        submitLabel="Create"
      />
    </div>
  )
}

export default Upload;
