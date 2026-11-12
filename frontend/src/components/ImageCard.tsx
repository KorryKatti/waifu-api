import { ImageDto, Role, ReviewStatus } from "../types";
import { Heart, Trash2, Edit2, FolderMinus, FolderPlus, GitPullRequest } from "lucide-react";
import { Link } from "react-router-dom";
import TagChip from "./TagChip";
import { useAuth } from "../context/AuthContext";
import { useMyPendingImageEdits } from "../hooks/useMyPendingImageEdits";
import { useAuthGuard } from "../hooks/useRequireAuth";
import { useState, useEffect } from "react";
import api from "../services/api";
import AlbumSelectionModal from "./modals/AlbumSelectionModal";
import { imgCard } from "../utils/cfImage";

interface ImageCardProps {
  image: ImageDto;
  onDelete?: (id: number) => void;
  onRemove?: (id: number) => void;
  onEdit?: (image: ImageDto) => void;
  onSuggest?: (image: ImageDto) => void;
  forceOverlay?: boolean;
  readOnly?: boolean;
  showTags?: boolean;
}

const ImageCard = ({ image, onDelete, onRemove, onEdit, onSuggest, forceOverlay = false, readOnly = false, showTags = false }: ImageCardProps) => {
  const { user } = useAuth();
  const checkAuth = useAuthGuard();
  const [isLiked, setIsLiked] = useState(!!image.likedAt);
  const [likesCount, setLikesCount] = useState(image.favorites);
  const [isLikeLoading, setIsLikeLoading] = useState(false);
  const [isImageLoaded, setIsImageLoaded] = useState(false);
  const [localAlbums, setLocalAlbums] = useState(image.albums || []);
  const [isAlbumModalOpen, setIsAlbumModalOpen] = useState(false);

  const isAdmin = user && user.role === Role.Admin;
  const { data: pendingEdits } = useMyPendingImageEdits(showTags);
  const edit = pendingEdits?.get(image.id);
  const removedTagIds = new Set((edit?.removeTags ?? []).map(t => t.id));
  const addedTags = edit?.addTags ?? [];

  const allTags = image.tags ?? [];
  const removedTags = allTags.filter(t => removedTagIds.has(t.id));
  const myPendingTags = allTags.filter(t => t.reviewStatus === ReviewStatus.Pending && !removedTagIds.has(t.id));
  const acceptedTags = allTags.filter(t => t.reviewStatus !== ReviewStatus.Pending && !removedTagIds.has(t.id));
  const TAG_CAP = 5;
  const shownTags = acceptedTags.slice(0, TAG_CAP);
  const hiddenTagCount = acceptedTags.length - shownTags.length;
  const hasAnyTag = acceptedTags.length || myPendingTags.length || removedTags.length || addedTags.length;

  useEffect(() => {
    setIsLiked(!!image.likedAt);
    setLikesCount(image.favorites);
    setLocalAlbums(image.albums || []);
  }, [image.likedAt, image.favorites, image.albums]);

  const toggleLike = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (!checkAuth('You must be logged in to like images.', `/images/${image.id}`)) return;
    if (isLikeLoading) return;
    setIsLikeLoading(true);
    const prevLiked = isLiked;
    setIsLiked(!prevLiked);
    setLikesCount(prev => prevLiked ? prev - 1 : prev + 1);
    try {
      if (prevLiked) {
        await api.delete(`/users/me/albums/favorites/images/${image.id}`);
        setLocalAlbums(prev => prev.filter(a => !a.isDefault));
      } else {
        await api.post(`/users/me/albums/favorites/images/${image.id}`);
      }
    } catch (error) {
      setIsLiked(prevLiked);
      setLikesCount(prev => prevLiked ? prev : prev - 1);
    } finally {
      setIsLikeLoading(false);
    }
  };

  const handleAlbumUpdate = (updatedImage: ImageDto) => {
    setIsLiked(!!updatedImage.likedAt);
    setLikesCount(updatedImage.favorites);
    setLocalAlbums(updatedImage.albums || []);
    image.albums = updatedImage.albums;
    image.likedAt = updatedImage.likedAt;
    image.favorites = updatedImage.favorites;
  };

  const showActions = forceOverlay;

  return (
    <div className="booru-panel group relative text-xs">
      <div className="relative">
        <Link to={`/images/${image.id}`} className="block bg-secondary/40">
          <div className="relative w-full h-36 sm:h-40 overflow-hidden flex items-center justify-center">
            {!isImageLoaded && <div className="absolute inset-0 bg-muted animate-pulse" />}
            <img
              src={imgCard(image.url)}
              alt={`Post ${image.id}`}
              loading="lazy"
              onLoad={() => setIsImageLoaded(true)}
              className={`max-w-full max-h-full w-auto h-auto object-contain transition-opacity duration-200 ${isImageLoaded ? 'opacity-100' : 'opacity-0'}`}
            />
          </div>
        </Link>

        {/* actions: always visible on touch/small, hover on desktop */}
        <div className={`absolute top-1 right-1 gap-1 ${showActions ? 'flex' : 'flex md:hidden md:group-hover:flex'}`}>
          {!readOnly && user && (
            <>
              <button
                className="p-1 bg-card/90 border border-border text-foreground hover:bg-secondary"
                title="Add to album"
                onClick={(e) => { e.preventDefault(); e.stopPropagation(); setIsAlbumModalOpen(true); }}
              >
                <FolderPlus size={12} />
              </button>
              <AlbumSelectionModal
                isOpen={isAlbumModalOpen}
                onClose={() => setIsAlbumModalOpen(false)}
                image={{ ...image, albums: localAlbums }}
                onUpdate={handleAlbumUpdate}
              />
            </>
          )}
          {!readOnly && onRemove && (
            <button onClick={(e) => { e.preventDefault(); e.stopPropagation(); onRemove(image.id); }} className="p-1 bg-card/90 border border-border hover:bg-secondary" title="Remove from album">
              <FolderMinus size={12} />
            </button>
          )}
          {onEdit && (
            <button onClick={(e) => { e.preventDefault(); e.stopPropagation(); onEdit(image); }} className="p-1 bg-card/90 border border-border hover:bg-secondary" title="Edit">
              <Edit2 size={12} />
            </button>
          )}
          {onSuggest && (
            <button onClick={(e) => { e.preventDefault(); e.stopPropagation(); onSuggest(image); }} className="p-1 bg-card/90 border border-border hover:bg-secondary" title="Suggest edit">
              <GitPullRequest size={12} />
            </button>
          )}
          {onDelete && isAdmin && (
            <button onClick={(e) => { e.preventDefault(); e.stopPropagation(); onDelete(image.id); }} className="p-1 bg-red-600/90 text-white border border-red-700 hover:bg-red-700" title="Delete">
              <Trash2 size={12} />
            </button>
          )}
        </div>

        <div className="px-1.5 py-1 border-t border-border bg-card flex items-center justify-between gap-2">
          <Link to={`/images/${image.id}`} className="booru-link font-mono">#{image.id}</Link>
          <div className="flex items-center gap-2 text-muted-foreground">
            {image.isNsfw && <span className="text-red-600 font-semibold" title="NSFW">E</span>}
            {image.isAnimated && <span className="font-semibold" title="Animated">GIF</span>}
            {readOnly ? (
              <span className="inline-flex items-center gap-0.5"><Heart size={11} /> {likesCount}</span>
            ) : (
              <button onClick={toggleLike} className="inline-flex items-center gap-0.5 hover:text-foreground" title="Favorite">
                <Heart size={11} className={isLiked ? 'fill-rose-500 text-rose-500' : ''} /> {likesCount}
              </button>
            )}
          </div>
        </div>
      </div>

      {showTags && (
        <div className="px-1.5 py-1.5 flex flex-wrap gap-1 border-t border-border bg-secondary/20">
          {shownTags.map(t => (
            <TagChip key={`t-${t.id}`} label={t.name} href={`/gallery?includedTags=${encodeURIComponent(t.slug)}`} />
          ))}
          {hiddenTagCount > 0 && (
            <TagChip label={`+${hiddenTagCount}`} href={`/images/${image.id}`} title="See all tags" />
          )}
          {addedTags.map(t => (
            <TagChip key={`add-${t.id}`} tone="add" label={t.name} href={`/gallery?includedTags=${encodeURIComponent(t.slug)}`} title="You proposed adding this tag" />
          ))}
          {removedTags.map(t => (
            <TagChip key={`rm-${t.id}`} tone="remove" label={t.name} title="You proposed removing this tag" />
          ))}
          {myPendingTags.map(t => (
            <TagChip key={`p-${t.id}`} tone="pending" label={t.name} title="Your tag (awaiting review)" />
          ))}
          {!hasAnyTag && <span className="text-muted-foreground italic">no tags</span>}
        </div>
      )}
    </div>
  );
};

export default ImageCard;
