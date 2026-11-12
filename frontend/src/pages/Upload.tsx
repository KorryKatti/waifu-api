import { useState, useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import api from '../services/api';
import SearchableSelect from '../components/SearchableSelect';
import { Info, Loader2, X, RotateCcw, Plus } from 'lucide-react';
import TagModal from '../components/modals/TagModal';
import ArtistModal from '../components/modals/ArtistModal';
import { useMetadata } from '../hooks/useMetadata';
import { ImageDto, Role } from '../types';
import { useRequireAuth } from '../hooks/useRequireAuth';
import { MetaTags } from '../hooks/useMetaTags';
import { getEnv } from '../utils/env';

interface BatchFile {
  id: string;
  file: File;
  previewUrl: string;
  source: string;
  status: 'queued' | 'uploading' | 'succeeded' | 'failed' | 'canceled';
  error?: string;
  imageId?: number;
  progress?: number;
  precheckError?: string;
}

const Upload = () => {
  const user = useRequireAuth();
  const navigate = useNavigate();

  const [files, setFiles] = useState<BatchFile[]>([]);
  const [isUploading, setIsUploading] = useState(false);
  const [agreedToTos, setAgreedToTos] = useState(false);
  const [sharedIsNsfw, setSharedIsNsfw] = useState(false);
  const [locked, setLocked] = useState(false);
  const [currentIndex, setCurrentIndex] = useState<number>(-1);
  const [liveAnnouncement, setLiveAnnouncement] = useState('');
  const cancelRef = useRef(false);
  const delayRef = useRef<number | null>(null);

  // persist metadata draft (not files)
  useEffect(() => {
    try {
      const raw = localStorage.getItem('upload_batch_draft');
      if (!raw) return;
      const draft = JSON.parse(raw);
      if (typeof draft.isNsfw === 'boolean') setSharedIsNsfw(draft.isNsfw);
      if (typeof draft.agreedToTos === 'boolean') setAgreedToTos(draft.agreedToTos);
    } catch {}
  }, []);
  useEffect(() => {
    try {
      localStorage.setItem('upload_batch_draft', JSON.stringify({ isNsfw: sharedIsNsfw, agreedToTos }));
    } catch {}
  }, [sharedIsNsfw, agreedToTos]);

  const tosUrl = getEnv('VITE_TOS_URL');
  const isReviewMode = !(user?.role === Role.Moderator || user?.role === Role.Admin);

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

  const addFiles = (fileList: FileList | null) => {
    if (!fileList || locked || isUploading) return;
    const incoming = Array.from(fileList);
    setFiles(prev => {
      const toAdd: BatchFile[] = [];
      const maxSizeMb = 10; // conservative client hint; backend enforces actual limit
      for (const file of incoming) {
        if (prev.length + toAdd.length >= 10) break;
        if (prev.some(p => p.file.name === file.name && p.file.size === file.size) || toAdd.some(p => p.file.name === file.name && p.file.size === file.size)) {
          continue;
        }
        let precheckError: string | undefined;
        if (!file.type.startsWith('image/')) precheckError = 'Not an image file.';
        else if (file.size <= 0) precheckError = 'File is empty.';
        else if (file.size > maxSizeMb * 1024 * 1024) precheckError = `File is too large (max ~${maxSizeMb}MB client hint).`;

        toAdd.push({
          id: `${file.name}-${file.size}-${Date.now()}-${Math.random()}`,
          file,
          previewUrl: URL.createObjectURL(file),
          source: '',
          status: precheckError ? 'failed' : 'queued',
          progress: 0,
          precheckError,
          error: precheckError,
        });
      }
      return [...prev, ...toAdd].slice(0, 10);
    });
  };

  const removeFile = (id: string) => {
    if (locked || isUploading) return;
    setFiles(prev => {
      const toRemove = prev.find(f => f.id === id);
      if (toRemove) URL.revokeObjectURL(toRemove.previewUrl);
      return prev.filter(f => f.id !== id);
    });
  };

  const updateSource = (id: string, source: string) => {
    if (locked) return;
    setFiles(prev => prev.map(f => f.id === id ? { ...f, source } : f));
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
            // try HTTP date? skip, use fallback
          }
          return status === 503 ? 5000 : 10000;
        }
        if (status === 409) {
          return 1000; // conflict/duplicate - shorter delay before moving on
        }
      }
      // network/timeout
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

  const uploadBatch = async () => {
    if (files.length === 0) return;
    if (tosUrl && !agreedToTos) return;
    // validate sources before start (basic URL check if provided)
    const invalid = files.find(f => f.source && f.source.trim() && !/^https?:\/\//i.test(f.source.trim()));
    if (invalid) {
      setLiveAnnouncement(`Invalid source URL for ${invalid.file.name}. Please fix before starting.`);
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
        // skip precheck-failed items
        continue;
      }
      setCurrentIndex(i);
      setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, status: 'uploading', error: undefined } : f));
      setLiveAnnouncement(`Uploading ${i + 1} of ${snapshot.length}: ${snapshot[i].file.name}`);

      const item = snapshot[i];
      const formData = new FormData();
      formData.append('file', item.file);

      selectedTags.forEach(tag => formData.append('tags', tag.slug || slugify(tag.name.trim())));
      selectedArtists.forEach(artist => formData.append('artists', String(artist.id)));

      if (item.source && item.source.trim()) {
        formData.append('source', item.source.trim());
      }
      formData.append('isNsfw', String(sharedIsNsfw));

      try {
        const { data: uploadedImage } = await api.post<ImageDto>('/images/upload', formData, {
          headers: { 'Content-Type': 'multipart/form-data' },
          onUploadProgress: (evt) => {
            if (!evt.total) return;
            const pct = Math.round((evt.loaded / evt.total) * 100);
            setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, progress: pct } : f));
          }
        });
        setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, status: 'succeeded', imageId: uploadedImage.id, progress: 100 } : f));
        setLiveAnnouncement(`Uploaded ${snapshot[i].file.name} successfully.`);
      } catch (e: any) {
        const msg = formatUploadError(e);
        setFiles(prev => prev.map((f, idx) => idx === i ? { ...f, status: 'failed', error: msg, progress: 0 } : f));
        setLiveAnnouncement(`Failed to upload ${snapshot[i].file.name}: ${msg}`);
        // apply backoff before next
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
    for (let j = 0; j < failedIndices.length; j++) {
      if (cancelRef.current) break;
      const { idx } = failedIndices[j];
      setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'uploading', error: undefined, progress: 0 } : f));
      setLiveAnnouncement(`Retrying ${files[idx].file.name}`);
      const item = files[idx];
      const formData = new FormData();
      formData.append('file', item.file);
      selectedTags.forEach(tag => formData.append('tags', tag.slug || slugify(tag.name.trim())));
      selectedArtists.forEach(artist => formData.append('artists', String(artist.id)));
      if (item.source && item.source.trim()) formData.append('source', item.source.trim());
      formData.append('isNsfw', String(sharedIsNsfw));
      try {
        const { data: uploadedImage } = await api.post<ImageDto>('/images/upload', formData, {
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
    setIsUploading(true);
    cancelRef.current = false;
    setFiles(prev => prev.map((f, i) => i === idx ? { ...f, status: 'uploading', error: undefined, progress: 0 } : f));
    setLiveAnnouncement(`Retrying ${files[idx].file.name}`);
    const item = files[idx];
    const formData = new FormData();
    formData.append('file', item.file);
    selectedTags.forEach(tag => formData.append('tags', tag.slug || slugify(tag.name.trim())));
    selectedArtists.forEach(artist => formData.append('artists', String(artist.id)));
    if (item.source && item.source.trim()) formData.append('source', item.source.trim());
    formData.append('isNsfw', String(sharedIsNsfw));
    try {
      const { data: uploadedImage } = await api.post<ImageDto>('/images/upload', formData, {
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
        description="Submit images in batches (up to 10). Uploads are reviewed before they go live."
        noindex
      />
      <div className="sr-only" aria-live="polite" role="status">{liveStatus}</div>
      {/* Review Notice */}
      <div className="bg-blue-500/10 border border-blue-500/20 rounded-xl p-4 mb-8 flex items-start gap-3">
        <Info className="text-blue-500 shrink-0 mt-0.5" size={20} />
        <div>
          <h4 className="font-bold text-blue-600 dark:text-blue-400 text-sm">Review Process</h4>
          <p className="text-sm text-muted-foreground mt-1">
            New uploads require review approval before appearing publicly. Batch uploads are sent one-by-one with a short delay.
          </p>
          <p className="text-sm text-muted-foreground mt-1">
            If an upload fails, try converting your image to WebP:{' '}
            <a href="https://cloudconvert.com/webp-converter" target="_blank" rel="noopener noreferrer" className="text-blue-600 dark:text-blue-400 hover:underline font-medium">
              cloudconvert.com/webp-converter
            </a>
            .
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

          <div className="space-y-2 max-h-[400px] overflow-auto">
            {files.map((f, idx) => (
              <div key={f.id} className="flex items-center gap-3 p-3 border border-border rounded-xl bg-card">
                <img src={f.previewUrl} alt="preview" className="w-16 h-16 object-contain rounded-lg bg-secondary" />
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <p className="text-sm font-medium truncate">{f.file.name}</p>
                      <p className="text-xs text-muted-foreground">{(f.file.size / 1024).toFixed(1)} KB</p>
                    </div>
                    <span className={`text-xs px-2 py-0.5 rounded-full whitespace-nowrap ${f.status === 'succeeded' ? 'bg-green-500/10 text-green-600' : f.status === 'failed' ? 'bg-red-500/10 text-red-600' : f.status === 'uploading' ? 'bg-blue-500/10 text-blue-600' : f.status === 'canceled' ? 'bg-yellow-500/10 text-yellow-600' : 'bg-secondary text-muted-foreground'}`}>
                      {f.status}{idx === currentIndex && isUploading ? ` ${f.progress ?? 0}%` : ''}
                    </span>
                  </div>
                  {f.status === 'uploading' && (
                    <div className="w-full h-1.5 bg-secondary rounded overflow-hidden" aria-label={`Upload progress for ${f.file.name}`}>
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
                    aria-label={`Source URL for ${f.file.name}`}
                  />
                  {f.error && <p className="text-xs text-destructive" title={f.error}>{f.error}</p>}
                  {f.imageId && <p className="text-xs text-muted-foreground">ID: {f.imageId}</p>}
                  {f.status === 'failed' && !isUploading && !f.precheckError && (
                    <button onClick={() => retrySingle(f.id)} className="text-xs text-primary hover:underline mt-1 inline-flex items-center gap-1" aria-label={`Retry upload for ${f.file.name}`}>
                      <RotateCcw size={12} /> Retry
                    </button>
                  )}
                </div>
                {!locked && (
                  <button onClick={() => removeFile(f.id)} className="p-2 hover:bg-secondary rounded-lg" aria-label={`Remove ${f.file.name}`}>
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
              <label className="flex items-center gap-3 p-4 border border-border rounded-xl cursor-pointer hover:bg-secondary/50 transition-colors">
                <input type="checkbox" checked={sharedIsNsfw} onChange={(e) => setSharedIsNsfw(e.target.checked)} disabled={locked} className="w-5 h-5 rounded text-destructive focus:ring-destructive" />
                <div><span className="block font-bold">NSFW Content (shared)</span><span className="text-xs text-muted-foreground">Applies to all images in batch</span></div>
              </label>
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
