import { useState, useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useParams, useNavigate, Link } from 'react-router-dom';
import api from '../services/api';
import { ImageDto, Role, ImageFormData, ReviewStatus, ReviewStatusFilter, ReviewableContentType } from '../types';
import { Heart, Trash2, Edit, FolderPlus, Flag, Image, GitPullRequest, Clock, Plus, ExternalLink } from 'lucide-react';
import { useMyPendingImageEdits } from '../hooks/useMyPendingImageEdits';
import { invalidateNavBadges } from '../hooks/useNavBadges';
import { useAuth } from '../context/AuthContext';
import { useNotification } from '../context/NotificationContext';
import { useNsfwConsent } from '../hooks/useNsfwConsent';
import { useImageUpdate } from '../hooks/useImageUpdate';
import { useAuthGuard } from '../hooks/useRequireAuth';
import ImageModal from '../components/modals/ImageModal';
import ConfirmModal from '../components/modals/ConfirmModal';
import ReasonModal from '../components/modals/ReasonModal';
import AlbumSelectionModal from '../components/modals/AlbumSelectionModal';
import NsfwWarning from '../components/NsfwWarning';
import MoreLikeThis from '../components/MoreLikeThis';
import NotFound from './NotFound';
import { MetaTags } from '../hooks/useMetaTags';
import { isBot } from '../utils/bot';
import { imgCard } from '../utils/cfImage';

const ImagePage = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { user } = useAuth();
  const { showNotification } = useNotification();
  const checkAuth = useAuthGuard();
  const queryClient = useQueryClient();
  const { data: pendingEdits } = useMyPendingImageEdits(!!user);

  const [image, setImage] = useState<ImageDto | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);

  const { showWarning: showNsfwWarning, setShowWarning: setShowNsfwWarning, grantConsent, hasConsent } = useNsfwConsent(false);
  const { updateImage } = useImageUpdate();

  const [showEditModal, setShowEditModal] = useState(false);
  const [showSuggestModal, setShowSuggestModal] = useState(false);
  const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false);
  const [isReportModalOpen, setIsReportModalOpen] = useState(false);
  const [isAlbumModalOpen, setIsAlbumModalOpen] = useState(false);

  const isAdminOrModerator = user && (user.role === Role.Admin || user.role === Role.Moderator);

  const fetchImage = async (silent = false) => {
    try {
      if (!silent) { setIsLoading(true); setError(null); setNotFound(false); }
      const params: Record<string, string> = {};
      if (isAdminOrModerator) {
        params.childrenReviewStatus = ReviewStatusFilter.All;
      } else if (user) {
        params.includeMyPendingChildren = 'true';
      }
      const { data } = await api.get<ImageDto>(`/images/${id}`, { params, skipGlobalErrorHandler: true });
      setImage(data);

      if (data.isNsfw && !silent && !hasConsent() && !isBot()) {
        setShowNsfwWarning(true);
      }
    } catch (err: any) {
      if (err?.response?.status === 404) {
        if (!silent) setNotFound(true);
      } else {
        console.error('Failed to fetch image', err);
        if (!silent) setError('Failed to load image.');
      }
    } finally {
      if (!silent) setIsLoading(false);
    }
  };

  useEffect(() => {
    fetchImage();
  }, [id, user?.id]);

  const handleDelete = async () => {
    try {
      await api.delete(`/images/${id}`);
      showNotification('success', 'Image deleted successfully');
      navigate('/gallery');
    } catch (err) {
      showNotification('error', 'Failed to delete image');
    }
  };

  const handleLike = async () => {
    if (!checkAuth('You must be logged in to like images.')) return;
    if (!image) return;
    try {
      if (image.likedAt) {
        await api.delete(`/users/${user!.id}/albums/favorites/images/${image.id}`, { skipGlobalErrorHandler: true });
        setImage(prev => prev ? { ...prev, likedAt: undefined, favorites: prev.favorites - 1 } : null);
      } else {
        await api.post(`/users/${user!.id}/albums/favorites/images/${image.id}`, {}, { skipGlobalErrorHandler: true });
        setImage(prev => prev ? { ...prev, likedAt: new Date().toISOString(), favorites: prev.favorites + 1 } : null);
      }
      fetchImage(true);
    } catch (err) { console.error(err); }
  };

  const handleSaveEdit = async (data: ImageFormData) => {
    if (!isAdminOrModerator || !image) return;
    try {
      const updatedImage = await updateImage(image.id, data);
      setImage(updatedImage);
      setShowEditModal(false);
      showNotification('success', 'Image updated successfully');
      fetchImage(true);
    } catch (err) {
      console.error('Failed to update image', err);
    }
  };

  const handleReport = async (description: string) => {
    if (!image) return;
    try {
      await api.post('/reports', { imageId: image.id, description });
      showNotification('success', 'Report submitted');
      setIsReportModalOpen(false);
      invalidateNavBadges(queryClient);
    } catch (err) {
      // handled globally
    }
  };

  const getReviewStatusLabel = (status: ReviewStatus) => {
    switch (status) {
      case ReviewStatus.Pending: return 'Pending';
      case ReviewStatus.Accepted: return 'Accepted';
      case ReviewStatus.Rejected: return 'Rejected';
      default: return 'Unknown';
    }
  };

  if (isLoading) return <div className="p-6 text-center text-muted-foreground">Loading...</div>;
  if (notFound) return <NotFound />;
  if (error || !image) return <div className="p-6 text-center text-destructive">{error || 'Not Found'}</div>;

  const edit = pendingEdits?.get(image.id);
  const removedTagIds = new Set((edit?.removeTags ?? []).map(t => t.id));
  const removedArtistIds = new Set((edit?.removeArtists ?? []).map(a => a.id));
  const addedTags = edit?.addTags ?? [];
  const addedArtists = edit?.addArtists ?? [];

  const tagNames = image.tags.map(t => t.name).join(', ');
  const artistNames = image.artists.map(a => a.name).join(', ');
  const metaDescription = [
    artistNames && `By ${artistNames}`,
    tagNames && `Tags: ${tagNames}`,
    `${image.width}x${image.height}`,
  ].filter(Boolean).join(' | ');

  const saucenaoImageUrl = imgCard(image.url);
  const saucenaoUrl = `https://saucenao.com/search.php?db=999&url=${encodeURIComponent(saucenaoImageUrl)}`;
  const waifu2xUrl = `https://waifu2x.booru.pics/Home/fromlink?denoise=1&scale=2&url=${encodeURIComponent(image.url)}`;

  return (
    <div className="px-3 md:px-4 py-3">
      <MetaTags
        title={`Image #${image.id}`}
        description={metaDescription}
        image={image.url}
        imageWidth={image.width}
        imageHeight={image.height}
        type="article"
        isNsfw={image.isNsfw}
      />

      <div className="flex flex-col md:flex-row gap-3 items-start">
        {/* Left booru sidebar: info, actions, tags */}
        <aside className="w-full md:w-64 md:shrink-0 space-y-3 md:sticky md:top-[calc(3rem+2.2rem)]">
          <div className="booru-panel">
            <div className="px-2 py-1.5 border-b border-border bg-secondary/30 font-bold text-sm">Post #{image.id}</div>
            <div className="p-2 space-y-1.5 text-sm">
              <div className="flex justify-between gap-2"><span className="text-muted-foreground">ID</span><span className="font-mono">{image.id}</span></div>
              <div className="flex justify-between gap-2"><span className="text-muted-foreground">Rating</span><span className={image.isNsfw ? 'text-red-600 font-semibold' : ''}>{image.isNsfw ? 'Explicit' : 'Safe'}</span></div>
              <div className="flex justify-between gap-2"><span className="text-muted-foreground">Status</span><span>{getReviewStatusLabel(image.reviewStatus)}</span></div>
              <div className="flex justify-between gap-2"><span className="text-muted-foreground">Size</span><span>{image.width}×{image.height}</span></div>
              <div className="flex justify-between gap-2"><span className="text-muted-foreground">File</span><span>{(image.byteSize / (1024 * 1024)).toFixed(2)} MB</span></div>
              <div className="flex justify-between gap-2"><span className="text-muted-foreground">Favorites</span><span>{image.favorites}</span></div>
              <div className="flex justify-between gap-2"><span className="text-muted-foreground">Uploaded</span><span>{new Date(image.uploadedAt).toLocaleDateString()}</span></div>
              {image.source && (
                <div className="pt-1 border-t border-border">
                  <span className="text-muted-foreground block mb-0.5">Source</span>
                  <a href={image.source} target="_blank" rel="noopener noreferrer" className="booru-link break-all inline-flex items-start gap-1">
                    <ExternalLink size={12} className="mt-0.5 shrink-0" />
                    <span className="line-clamp-2">{image.source}</span>
                  </a>
                </div>
              )}
            </div>
          </div>

          <div className="booru-panel">
            <div className="px-2 py-1.5 border-b border-border bg-secondary/30 font-bold text-sm">Actions</div>
            <div className="p-2 flex flex-wrap gap-1.5">
              <button onClick={handleLike} className="inline-flex items-center gap-1 px-2 py-1 text-xs border border-border bg-card hover:bg-secondary rounded-sm">
                <Heart size={13} className={image.likedAt ? 'text-red-500 fill-current' : ''} /> {image.favorites}
              </button>
              <button onClick={() => setIsAlbumModalOpen(true)} className="inline-flex items-center gap-1 px-2 py-1 text-xs border border-border bg-primary text-primary-foreground hover:opacity-90 rounded-sm">
                <FolderPlus size={13} /> Album
              </button>
              <button
                onClick={() => { if (!checkAuth('You must be logged in to report images.')) return; setIsReportModalOpen(true); }}
                className="inline-flex items-center gap-1 px-2 py-1 text-xs border border-border bg-card hover:bg-secondary rounded-sm"
              >
                <Flag size={13} /> Report
              </button>
              {user && !isAdminOrModerator && (
                <button onClick={() => setShowSuggestModal(true)} className="inline-flex items-center gap-1 px-2 py-1 text-xs border border-border bg-card hover:bg-secondary rounded-sm">
                  <GitPullRequest size={13} /> Suggest edit
                </button>
              )}
              {isAdminOrModerator && (
                <>
                  <button onClick={() => setShowEditModal(true)} className="inline-flex items-center gap-1 px-2 py-1 text-xs border border-border bg-accent hover:bg-accent/80 rounded-sm">
                    <Edit size={13} /> Edit
                  </button>
                  <button onClick={() => setIsDeleteModalOpen(true)} className="inline-flex items-center gap-1 px-2 py-1 text-xs border border-destructive/40 bg-destructive/10 text-destructive hover:bg-destructive/20 rounded-sm">
                    <Trash2 size={13} /> Delete
                  </button>
                </>
              )}
            </div>
          </div>

          {image.url && (
            <div className="booru-panel">
              <div className="px-2 py-1.5 border-b border-border bg-secondary/30 font-bold text-sm">Tools</div>
              <div className="p-2 flex flex-col gap-1.5 text-sm">
                <a
                  href={saucenaoUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="booru-link inline-flex items-center gap-1.5"
                  title="Search image source on SauceNAO"
                >
                  <ExternalLink size={13} /> SauceNAO
                </a>
                <a
                  href={waifu2xUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="booru-link inline-flex items-center gap-1.5"
                  title="Upscale/denoise with Waifu2x"
                >
                  <ExternalLink size={13} /> Waifu2x
                </a>
              </div>
            </div>
          )}

          <div className="booru-panel">
            <div className="px-2 py-1.5 border-b border-border bg-secondary/30 font-bold text-sm">Artists</div>
            <ul className="p-2 space-y-1 text-sm">
              {image.artists.length === 0 && addedArtists.length === 0 && <li className="text-muted-foreground italic">None</li>}
              {image.artists.map(artist => {
                const removed = removedArtistIds.has(artist.id);
                const pending = artist.reviewStatus === ReviewStatus.Pending;
                return (
                  <li key={artist.id} className="flex items-center gap-1.5">
                    <Link to={`/gallery?includedArtists=${artist.id}`} className="text-muted-foreground hover:text-foreground" title="Posts by artist"><Image size={13} /></Link>
                    <Link to={`/artists?includedIds=${artist.id}`} className={`booru-link tag-artist ${removed ? 'line-through opacity-60' : ''} ${pending ? 'text-amber-600' : ''}`}>
                      {pending && <Clock size={11} className="inline mr-1" />}{artist.name}
                    </Link>
                  </li>
                );
              })}
              {addedArtists.map(a => (
                <li key={`add-artist-${a.id}`} className="flex items-center gap-1.5 text-green-700 dark:text-green-400">
                  <Plus size={13} />
                  <Link to={`/gallery?includedArtists=${a.id}`} className="hover:underline">{a.name}</Link>
                </li>
              ))}
            </ul>
          </div>

          <div className="booru-panel">
            <div className="px-2 py-1.5 border-b border-border bg-secondary/30 font-bold text-sm">Tags</div>
            <ul className="p-2 space-y-0.5 text-sm">
              {image.tags.length === 0 && addedTags.length === 0 && <li className="text-muted-foreground italic">None</li>}
              {image.tags.map(tag => {
                const removed = removedTagIds.has(tag.id);
                const pending = tag.reviewStatus === ReviewStatus.Pending;
                return (
                  <li key={tag.id} className="flex items-center gap-1.5">
                    <Link to={`/gallery?includedTags=${encodeURIComponent(tag.slug)}`} className="text-muted-foreground hover:text-foreground" title="Posts with tag"><Image size={13} /></Link>
                    <Link to={`/tags?includedIds=${tag.id}`} className={`booru-link ${removed ? 'line-through opacity-60 text-destructive' : ''} ${pending ? 'text-amber-600' : ''}`}>
                      {pending && <Clock size={11} className="inline mr-1" />}{tag.name}
                    </Link>
                  </li>
                );
              })}
              {addedTags.map(t => (
                <li key={`add-tag-${t.id}`} className="flex items-center gap-1.5 text-green-700 dark:text-green-400">
                  <Plus size={13} />
                  <Link to={`/gallery?includedTags=${encodeURIComponent(t.slug)}`} className="hover:underline">{t.name}</Link>
                </li>
              ))}
            </ul>
          </div>
        </aside>

        {/* Main image area */}
        <div className="flex-1 min-w-0">
          <div className="booru-panel">
            <div className="relative bg-secondary/30 flex items-center justify-center min-h-[200px]">
              <img
                src={image.url}
                alt={`Post ${image.id}`}
                className="max-w-full h-auto object-contain"
              />
              {image.isNsfw && showNsfwWarning && (
                <NsfwWarning
                  variant="overlay"
                  onConsent={grantConsent}
                  onDecline={() => window.history.length > 1 ? navigate(-1) : navigate('/gallery')}
                />
              )}
            </div>
          </div>
        </div>
      </div>

      <MoreLikeThis image={image} />

      <ConfirmModal
        isOpen={isDeleteModalOpen}
        onClose={() => setIsDeleteModalOpen(false)}
        onConfirm={handleDelete}
        title="Delete Image"
        message="Permanently delete this image? This cannot be undone."
        confirmText="Delete"
        variant="destructive"
        zIndex={70}
      />

      <ImageModal
        isOpen={showEditModal}
        onClose={() => setShowEditModal(false)}
        initialData={image}
        onSubmit={handleSaveEdit}
        onDelete={() => setIsDeleteModalOpen(true)}
      />

      {showSuggestModal && (
        <ImageModal
          isOpen={showSuggestModal}
          onClose={() => {
            setShowSuggestModal(false);
            queryClient.invalidateQueries({ queryKey: ['my-pending-image-edits'] });
            invalidateNavBadges(queryClient);
          }}
          initialData={image}
          onSubmit={() => {}}
          suggestContext={{ targetType: ReviewableContentType.Image, targetId: image.id, current: image }}
        />
      )}

      <ReasonModal
        isOpen={isReportModalOpen}
        onClose={() => setIsReportModalOpen(false)}
        onSubmit={handleReport}
        title="Report Image"
        description="Please describe why you are reporting this image."
        placeholder="Reason for reporting..."
        submitText="Submit Report"
      />

      <AlbumSelectionModal
        isOpen={isAlbumModalOpen}
        onClose={() => setIsAlbumModalOpen(false)}
        image={image}
        onUpdate={(updated) => setImage(updated)}
      />
    </div>
  );
};

export default ImagePage;
