import { Link } from 'react-router-dom';
import { ArrowRight, Book, Wifi, WifiOff, Image as ImageIcon, Tag as TagIcon, Users, Activity, Copy, Check, Terminal } from 'lucide-react';
import { useEffect, useState } from 'react';
import api from '../services/api';
import { getEnv } from '../utils/env';
import Skeleton from '../components/Skeleton';
import { MetaTags } from '../hooks/useMetaTags';

interface PublicStats {
  totalRequests: number;
  totalImages: number;
  totalTags: number;
  totalArtists: number;
}

function formatCompactNumber(n: number): string {
  if (n >= 1_000_000_000) return `${+(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${+(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${+(n / 1_000).toFixed(1)}K`;
  return n.toString();
}

const Home = () => {
  const [isApiOnline, setIsApiOnline] = useState<boolean>(false);
  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [stats, setStats] = useState<PublicStats | null>(null);
  const [loadingStats, setLoadingStats] = useState<boolean>(true);
  const [copied, setCopied] = useState(false);

  const docsUrl = getEnv('VITE_DOCS_URL');
  const apiBase = (getEnv('VITE_API_URL') || '/api').replace(/\/$/, '');
  const apiOrigin = /^https?:\/\//.test(apiBase) ? apiBase : `${window.location.origin}${apiBase}`;
  const quickstart = `curl "${apiOrigin}/images?includedTags=waifu&isNsfw=false&pageSize=1"`;

  const copyQuickstart = () => {
    navigator.clipboard.writeText(quickstart).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }).catch(() => {});
  };

  useEffect(() => {
    const fetchHero = async () => {
      try {
        await api.get('/images', { params: { pageSize: 1, isNsfw: 'false' }, skipGlobalErrorHandler: true });
        setIsApiOnline(true);
      } catch (e) {
        console.error(e);
        setIsApiOnline(false);
      } finally {
        setIsLoading(false);
      }
    };

    const fetchStats = async () => {
      try {
        const { data } = await api.get<PublicStats>('/stats/public', { skipGlobalErrorHandler: true });
        setStats(data);
      } catch (e) {
        console.error(e);
      } finally {
        setLoadingStats(false);
      }
    };

    fetchHero();
    fetchStats();
  }, []);

  return (
    <div className="container mx-auto p-3 md:p-4 max-w-5xl">
      <MetaTags />

      <div className="booru-panel mb-3">
        <div className="px-3 py-2 border-b border-border bg-secondary/30 flex items-center justify-between gap-2 flex-wrap">
          <h1 className="text-xl font-bold">Waifu.im API</h1>
          <div className="inline-flex items-center gap-2 px-2 py-1 border border-border bg-card text-xs font-semibold">
            {isLoading ? (
              <><Skeleton className="w-3 h-3 rounded-full" /><Skeleton className="w-14 h-3" /></>
            ) : isApiOnline ? (
              <><Wifi size={12} className="text-emerald-500" /><span>API Online</span></>
            ) : (
              <><WifiOff size={12} className="text-destructive" /><span>API Offline</span></>
            )}
          </div>
        </div>
        <div className="p-3 md:p-4 space-y-4">
          <p className="text-muted-foreground">
            Browse and discover anime image posts, tags, artists, and collections. The REST API remains the core product.
          </p>

          <div className="flex flex-wrap gap-2">
            <Link to="/gallery" className="inline-flex items-center gap-2 px-4 py-2 bg-primary text-primary-foreground font-semibold rounded-sm hover:opacity-90">
              Browse posts <ArrowRight size={16} />
            </Link>
            {docsUrl && (
              <a href={docsUrl} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-2 px-4 py-2 bg-card border border-border font-semibold rounded-sm hover:bg-secondary">
                <Book size={16} /> API documentation
              </a>
            )}
          </div>

          <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
            <div className="booru-panel p-3">
              <div className="flex items-center gap-2 text-muted-foreground text-xs uppercase font-semibold mb-1"><Activity size={14} className="text-primary" /> Requests</div>
              {loadingStats ? <Skeleton className="h-7 w-20" /> : <div className="text-xl font-bold">{formatCompactNumber(stats?.totalRequests ?? 0)}</div>}
            </div>
            <div className="booru-panel p-3">
              <div className="flex items-center gap-2 text-muted-foreground text-xs uppercase font-semibold mb-1"><ImageIcon size={14} className="text-blue-500" /> Posts</div>
              {loadingStats ? <Skeleton className="h-7 w-16" /> : <div className="text-xl font-bold">{formatCompactNumber(stats?.totalImages ?? 0)}</div>}
            </div>
            <div className="booru-panel p-3">
              <div className="flex items-center gap-2 text-muted-foreground text-xs uppercase font-semibold mb-1"><TagIcon size={14} className="text-green-600" /> Tags</div>
              {loadingStats ? <Skeleton className="h-7 w-12" /> : <div className="text-xl font-bold">{formatCompactNumber(stats?.totalTags ?? 0)}</div>}
            </div>
            <div className="booru-panel p-3">
              <div className="flex items-center gap-2 text-muted-foreground text-xs uppercase font-semibold mb-1"><Users size={14} className="text-purple-500" /> Artists</div>
              {loadingStats ? <Skeleton className="h-7 w-12" /> : <div className="text-xl font-bold">{formatCompactNumber(stats?.totalArtists ?? 0)}</div>}
            </div>
          </div>

          <div className="booru-panel">
            <div className="px-3 py-2 border-b border-border bg-secondary/30 flex items-center gap-2 text-sm font-semibold">
              <Terminal size={14} /> Try the API
            </div>
            <div className="relative">
              <pre className="overflow-x-auto p-3 pr-12 text-xs sm:text-sm font-mono text-foreground"><code>{quickstart}</code></pre>
              <button
                onClick={copyQuickstart}
                aria-label={copied ? 'Copied' : 'Copy API command'}
                className="absolute top-2 right-2 p-1.5 rounded-sm border border-border bg-card hover:bg-secondary text-muted-foreground hover:text-foreground"
              >
                {copied ? <Check size={14} className="text-emerald-500" /> : <Copy size={14} />}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default Home;
