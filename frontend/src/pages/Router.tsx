import { useEffect, useRef, useState } from 'react';
import { AlertTriangle, Eye, EyeOff } from 'lucide-react';
import api from '../services/api';
import { MetaTags } from '../hooks/useMetaTags';
import { useNsfwConsent } from '../hooks/useNsfwConsent';
import NsfwWarning from '../components/NsfwWarning';
import { ImageDto, ImageOrderBy, NsfwMode, PaginatedList } from '../types';

// Mirrors the public shape of GET /instances (see Instances.tsx).
interface RouterInstance {
    url: string;
    name?: string;
    uptime: number | null;
    schema: boolean | null;
    images: boolean | null;
}

function pickBestInstance(instances: RouterInstance[]): RouterInstance | null {
    const healthy = instances.filter(i => i.images && i.schema !== false);
    const sorted = [...healthy].sort((a, b) => (b.uptime ?? -1) - (a.uptime ?? -1));
    // Prefer a community instance over our own API/CDN so the example
    // actually demonstrates routing away from us.
    return sorted.find(i => !i.url.includes('onrender.com') && !i.url.includes('waifu-cdn')) ?? sorted[0] ?? null;
}

const Router = () => {
    // Wall is off by default: no fetch, no background until toggled on.
    const [wallActive, setWallActive] = useState(false);
    const [loading, setLoading] = useState(false);
    const [wallImages, setWallImages] = useState<ImageDto[]>([]);
    const [exampleInstance, setExampleInstance] = useState<string | null>(null);
    // The wall is fetched unfiltered (isNsfw=All), so it stays blurred until
    // the user explicitly opts into seeing it as-is (behind the NSFW warning).
    const [isBlurred, setIsBlurred] = useState(true);
    const {
        showWarning: showNsfwWarning,
        setShowWarning: setShowNsfwWarning,
        grantConsent,
        dismissWarning,
        hasConsent,
    } = useNsfwConsent(false);
    const loadedRef = useRef(false);

    // Router, entirely in the browser: get instances, pick the best one,
    // then fetch random images from that instance's CDN.
    const loadExample = async () => {
        if (loadedRef.current || loading) return;
        setLoading(true);
        try {
            const { data } = await api.get<{ instances: RouterInstance[] }>('/instances', { skipGlobalErrorHandler: true });
            const best = pickBestInstance(data.instances ?? []);

            let items: ImageDto[] = [];
            let source: string | null = null;

            if (best) {
                try {
                    const controller = new AbortController();
                    const timeout = window.setTimeout(() => controller.abort(), 8000);
                    const res = await fetch(
                        `${best.url.replace(/\/+$/, '')}/images?isNsfw=All&orderBy=Random&pageSize=60`,
                        { signal: controller.signal },
                    );
                    window.clearTimeout(timeout);
                    if (!res.ok) throw new Error(`instance responded ${res.status}`);
                    const body = await res.json();
                    items = body.items ?? [];
                    source = best.url;
                } catch {
                    // Community instance may block CORS or be asleep; fall back
                    // to our own listing so the example still renders.
                    items = [];
                }
            }

            if (items.length === 0) {
                const fallback = await api.get<PaginatedList<ImageDto>>('/images', {
                    params: { isNsfw: NsfwMode.All, orderBy: ImageOrderBy.Random, pageSize: 60 },
                    skipGlobalErrorHandler: true,
                });
                items = fallback.data.items ?? [];
                source = 'our CDN (instance fetch unavailable)';
            }

            setWallImages(items.filter(img => img.url));
            setExampleInstance(source);
            loadedRef.current = true;
        } catch {
            setWallImages([]);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        if (wallActive) void loadExample();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [wallActive]);

    // Unblurring shows the unfiltered (isNsfw=All) wall as-is, so it requires
    // the same NSFW consent as the gallery: shown first, remembered after.
    const handleToggleBlur = () => {
        if (!isBlurred) {
            setIsBlurred(true);
            return;
        }
        if (hasConsent()) {
            setIsBlurred(false);
        } else {
            setShowNsfwWarning(true);
        }
    };

    const handleNsfwConsent = () => {
        grantConsent();
        setIsBlurred(false);
    };

    const handleNsfwDecline = () => {
        dismissWarning();
        setIsBlurred(true);
    };

    // Split into 3 rows for a TV-wall effect; each row scrolls the other way.
    const rows = [0, 1, 2].map(i => wallImages.filter((_, idx) => idx % 3 === i));
    const wallLoaded = wallActive && wallImages.length > 0;

    return (
        // Fill the whole content area (under the 4rem header, like Home/Login):
        // without a min-height this shrank to the hero text, so the wall only
        // covered the top half and the rest of the screen was left empty.
        // min-h (not h) so the page can still scroll if a site banner is shown.
        <div className="relative flex min-h-[calc(100vh-4rem)] flex-col overflow-hidden">
            <MetaTags
                title="Router"
                description="Finds the fastest community instance serving the image you asked for."
            />

            <style>{`
                @keyframes router-wall-scroll {
                    from { transform: translateX(0); }
                    to { transform: translateX(-50%); }
                }
                @keyframes router-wall-scroll-reverse {
                    from { transform: translateX(-50%); }
                    to { transform: translateX(0); }
                }
            `}</style>

            {wallLoaded && (
                <div className="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
                    <div
                        className={`flex h-full flex-col gap-3 transition-[filter,opacity,transform] duration-500 ${isBlurred ? 'opacity-50' : 'opacity-100'}`}
                        style={isBlurred
                            ? { filter: 'blur(10px) saturate(1.1)', transform: 'scale(1.08)' }
                            : undefined}
                    >
                        {rows.map((row, rowIdx) => {
                            const loop = [...row, ...row];
                            return (
                                // flex-1 + min-h-0: rows share the full height instead of
                                // being fixed h-36, so the wall covers the whole screen.
                                <div key={rowIdx} className="flex min-h-0 w-max flex-1 gap-3 pr-3"
                                    style={{
                                        animation: `${rowIdx % 2 === 0 ? 'router-wall-scroll' : 'router-wall-scroll-reverse'} ${90 + rowIdx * 25}s linear infinite`,
                                    }}>
                                    {loop.map((img, idx) => (
                                        <img
                                            key={`${img.id}-${idx}`}
                                            src={img.url}
                                            alt=""
                                            loading="lazy"
                                            decoding="async"
                                            draggable={false}
                                            className="h-full w-auto shrink-0 rounded-xl object-cover"
                                        />
                                    ))}
                                </div>
                            );
                        })}
                    </div>
                    {/* Dim layer so foreground text stays readable over the wall.
                        Heavy while blurred; only a light veil once unblurred so
                        the images are shown as they are. */}
                    <div className={`absolute inset-0 transition-opacity duration-500 ${isBlurred ? 'bg-background/70' : 'bg-background/20'}`} />
                </div>
            )}

            <div className="relative z-10 container mx-auto flex w-full max-w-5xl flex-1 flex-col justify-center px-4 py-12">
                <div className="text-center mb-10">
                    <span className="inline-flex items-center rounded-full border border-border bg-card px-3 py-1 text-xs font-medium text-muted-foreground">
                        Coming in v9.0.0
                    </span>
                    {!isBlurred && wallLoaded && (
                        <span className="ml-2 inline-flex items-center gap-1 rounded-full border border-destructive/40 bg-destructive/10 px-3 py-1 text-xs font-medium text-destructive">
                            <AlertTriangle size={12} />
                            Unblurred: may contain NSFW
                        </span>
                    )}
                    <h1 className="text-4xl font-black mt-4 mb-4 bg-gradient-to-r from-primary to-purple-600 bg-clip-text text-transparent">
                        Router
                    </h1>
                    <p className="text-lg text-muted-foreground max-w-2xl mx-auto">
                        Router will find the fastest community instance that has the image you asked for and return its URL directly, so anonymous clients can skip our CDN without us proxying the bytes.
                    </p>
                    <div className="mt-6">
                        <div className="flex flex-wrap items-center justify-center gap-3">
                            <button
                                type="button"
                                onClick={() => setWallActive(v => !v)}
                                disabled={loading}
                                className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-50"
                                aria-pressed={wallActive}
                            >
                                {loading ? 'Loading…' : wallActive ? 'Hide example' : 'See an example'}
                            </button>
                            {wallLoaded && (
                                <button
                                    type="button"
                                    onClick={handleToggleBlur}
                                    className={`inline-flex items-center gap-2 rounded-full px-5 py-2 text-sm font-medium transition-colors ${isBlurred
                                        ? 'border border-border bg-card text-foreground hover:bg-secondary'
                                        : 'border border-destructive/40 bg-destructive/10 text-destructive hover:bg-destructive/20'}`}
                                    aria-pressed={!isBlurred}
                                    title={isBlurred ? 'Show the example images as-is, without blur' : 'Blur the example images again'}
                                >
                                    {isBlurred ? <Eye size={16} /> : <EyeOff size={16} />}
                                    {isBlurred ? 'Unblur (show as-is)' : 'Re-blur'}
                                </button>
                            )}
                        </div>
                        <p className="mt-2 text-xs text-muted-foreground">
                            Images will be fetched from instance CDN and not necessarily ours.
                        </p>
                        {wallLoaded && (
                            <p className="mt-1 text-xs text-muted-foreground">
                                {isBlurred
                                    ? 'Example images are blurred and unfiltered (isNsfw=All): unblurring may show NSFW content.'
                                    : 'Example images are unfiltered (isNsfw=All) and may include NSFW content.'}
                            </p>
                        )}
                        {exampleInstance && wallActive && (
                            <p className="mt-1 break-words text-xs text-muted-foreground">
                                Example fetched from {exampleInstance}
                            </p>
                        )}
                    </div>
                </div>
            </div>

            {showNsfwWarning && (
                <NsfwWarning variant="modal" onConsent={handleNsfwConsent} onDecline={handleNsfwDecline} />
            )}
        </div>
    );
};

export default Router;
