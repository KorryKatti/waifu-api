import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import api from '../services/api';
import { buildQueryParams } from '../utils/queryParams';
import { useNsfwConsent } from '../hooks/useNsfwConsent';
import { ImageDto, ImageOrderBy, NsfwMode, PaginatedList } from '../types';
import ImageCard from './ImageCard';

interface MoreLikeThisProps {
  image: ImageDto;
}

const MAX_ITEMS = 12;
const FETCH_SIZE = 12;
const MAX_TAG_QUERIES = 3;

const fetchImages = async (params: {
  includedArtists?: string[];
  includedTags?: string[];
  excludedIds: string[];
  isNsfw: NsfwMode;
}) => {
  const searchParams = buildQueryParams({
    params: {
      page: 1,
      pageSize: FETCH_SIZE,
      orderBy: ImageOrderBy.UploadedAt,
      isNsfw: params.isNsfw,
    },
    arrays: {
      includedArtists: params.includedArtists,
      includedTags: params.includedTags,
      excludedIds: params.excludedIds,
    },
  });
  const { data } = await api.get<PaginatedList<ImageDto>>('/images', { params: searchParams });
  return data.items ?? [];
};

const MoreLikeThis = ({ image }: MoreLikeThisProps) => {
  const { hasConsent } = useNsfwConsent(false);
  const artistIds = image.artists?.map((a) => String(a.id)) ?? [];
  const tagSlugs = (image.tags ?? []).slice(0, MAX_TAG_QUERIES).map((t) => t.slug);
  const hasSignals = artistIds.length > 0 || tagSlugs.length > 0;

  // Only include NSFW results if the user has previously consented.
  const isNsfwMode = hasConsent() ? NsfwMode.All : NsfwMode.False;
  const excludedIds = [String(image.id)];

  const { data, isLoading, isError } = useQuery({
    queryKey: ['more-like-this', image.id, artistIds.join(','), tagSlugs.join(','), isNsfwMode],
    enabled: hasSignals,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    queryFn: async () => {
      const requests: Promise<ImageDto[]>[] = [];
      if (artistIds.length > 0) {
        requests.push(fetchImages({ includedArtists: artistIds, excludedIds, isNsfw: isNsfwMode }));
      }
      // Query tags individually to avoid over-constraining (multiple includedTags are typically ANDed).
      for (const slug of tagSlugs) {
        requests.push(fetchImages({ includedTags: [slug], excludedIds, isNsfw: isNsfwMode }));
      }

      const results = await Promise.allSettled(requests);
      const merged: ImageDto[] = [];
      const seen = new Set<number>();
      for (const r of results) {
        if (r.status !== 'fulfilled') continue;
        for (const item of r.value) {
          if (item.id === image.id || seen.has(item.id)) continue;
          seen.add(item.id);
          merged.push(item);
        }
      }

      const artistSet = new Set(artistIds);
      const tagSet = new Set((image.tags ?? []).map((t) => t.slug));
      merged.sort((a, b) => {
        const score = (img: ImageDto) => {
          const sharedArtists = (img.artists ?? []).filter((a) => artistSet.has(String(a.id))).length;
          const sharedTags = (img.tags ?? []).filter((t) => tagSet.has(t.slug)).length;
          return sharedArtists * 2 + sharedTags;
        };
        const diff = score(b) - score(a);
        if (diff !== 0) return diff;
        return (b.favorites ?? 0) - (a.favorites ?? 0);
      });

      return merged.slice(0, MAX_ITEMS);
    },
  });

  const seeMoreHref = useMemo(() => {
    const params = new URLSearchParams();
    if (artistIds.length > 0) {
      artistIds.forEach((id) => params.append('includedArtists', id));
    } else if (tagSlugs.length > 0) {
      params.append('includedTags', tagSlugs[0]);
    }
    params.set('isNsfw', isNsfwMode);
    params.set('orderBy', ImageOrderBy.UploadedAt);
    return `/gallery?${params.toString()}`;
  }, [artistIds, tagSlugs, isNsfwMode]);

  if (!hasSignals) return null;
  if (isError) return null;
  if (!isLoading && (!data || data.length === 0)) return null;

  return (
    <section className="mt-4" aria-label="More like this">
      <div className="booru-panel">
        <div className="px-2 py-1.5 border-b border-border bg-secondary/30 flex items-center justify-between gap-2">
          <h2 className="font-bold text-sm">More like this</h2>
          <Link to={seeMoreHref} className="booru-link text-sm">
            See more
          </Link>
        </div>
        <div className="p-2">
          {isLoading ? (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-2">
              {[...Array(6)].map((_, i) => (
                <div key={i} className="bg-muted h-36 sm:h-40 border border-border animate-pulse" />
              ))}
            </div>
          ) : (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-2 items-start">
              {data!.map((img) => (
                <ImageCard key={img.id} image={img} readOnly />
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  );
};

export default MoreLikeThis;
