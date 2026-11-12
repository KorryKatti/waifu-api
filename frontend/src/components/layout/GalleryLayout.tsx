import { useState, ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ImageDto, Role, ImageFormData, ImageOrderBy, ReviewableContentType } from '../../types';
import { useAuth } from '../../context/AuthContext';
import { useNotification } from '../../context/NotificationContext';
import { useImageUpdate } from '../../hooks/useImageUpdate';
import api from '../../services/api';
import ImageGrid from '../ImageGrid';
import Pagination from '../Pagination';
import FilterSidebar from '../FilterSidebar';
import FloatingRefreshButton from '../FloatingRefreshButton';
import ConfirmModal from '../modals/ConfirmModal';
import ImageModal from '../modals/ImageModal';
import { Option } from '../SearchableSelect';
import { Search, Tags, SlidersHorizontal } from 'lucide-react';
import { useTagView } from '../../hooks/useTagView';

interface GalleryLayoutProps {
  images: ImageDto[];
  isLoading: boolean;
  error?: any;
  refetch: () => void;
  page: number;
  totalPages: number;
  setPage: (page: number) => void;
  searchParams: URLSearchParams;
  setSearchParams: (params: URLSearchParams | ((prev: URLSearchParams) => URLSearchParams)) => void;
  headerContent?: ReactNode;
  sortOptions?: Option[];
  emptyState?: ReactNode;
  onRemoveFromAlbum?: (id: number) => void;
  readOnly?: boolean;
}

export const GalleryLayout = ({
  images,
  isLoading,
  error,
  refetch,
  page,
  totalPages,
  setPage,
  searchParams,
  setSearchParams,
  headerContent,
  sortOptions,
  emptyState,
  onRemoveFromAlbum,
  readOnly = false
}: GalleryLayoutProps) => {
  const { user } = useAuth();
  const { showNotification } = useNotification();
  const { updateImage } = useImageUpdate();
  const queryClient = useQueryClient();

  const { showTags, toggle: toggleTags } = useTagView();

  const [showFilters, setShowFilters] = useState(() => typeof window !== 'undefined' ? window.innerWidth >= 768 : false);
  const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false);
  const [imageToDelete, setImageToDelete] = useState<number | null>(null);
  const [editingImage, setEditingImage] = useState<ImageDto | null>(null);
  const [isEditModalOpen, setIsEditModalOpen] = useState(false);
  const [suggestingImage, setSuggestingImage] = useState<ImageDto | null>(null);
  const [isSuggestModalOpen, setIsSuggestModalOpen] = useState(false);

  const isAdmin = user?.role === Role.Admin;
  const isAdminOrModerator = user && (user.role === Role.Admin || user.role === Role.Moderator);

  const confirmDelete = (id: number) => {
    setImageToDelete(id);
    setIsDeleteModalOpen(true);
  };

  const handleRealDelete = async () => {
    if (!imageToDelete) return;
    try {
      await api.delete(`/images/${imageToDelete}`);
      refetch();
      setIsDeleteModalOpen(false);
      setImageToDelete(null);
      showNotification('success', 'Image deleted');
    } catch (e) { }
  };

  const handleSaveEdit = async (data: ImageFormData) => {
    if (!editingImage) return;
    try {
      await updateImage(editingImage.id, data);
      showNotification('success', 'Image updated');
      refetch();
      setIsEditModalOpen(false);
      setEditingImage(null);
    } catch (err) { }
  };

  const defaultSortOptions = [
    { id: ImageOrderBy.Random, name: 'Random' },
    { id: ImageOrderBy.UploadedAt, name: 'Newest' },
    { id: ImageOrderBy.Favorites, name: 'Most Popular' }
  ];

  const currentSort = searchParams.get('orderBy');

  return (
    <div className="flex flex-col md:flex-row min-h-[calc(100vh-6rem)] relative">
      {showFilters && (
        <div className="fixed inset-0 bg-black/40 z-50 md:hidden" onClick={() => setShowFilters(false)} />
      )}
      {/* Left booru-style tag/filter sidebar */}
      <FilterSidebar
        searchParams={searchParams}
        setSearchParams={setSearchParams}
        showFilters={showFilters}
        setShowFilters={setShowFilters}
        sortOptions={sortOptions || defaultSortOptions}
      />

      <div className="flex-1 min-w-0">
        <FloatingRefreshButton onRefresh={refetch} />

        <div className="p-3 md:p-4">
          <div className="booru-panel mb-3">
            <div className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 border-b border-border bg-secondary/30">
              <div className="min-w-0 flex-1">
                {headerContent || (
                  <h1 className="text-lg font-bold tracking-tight text-foreground">Posts</h1>
                )}
              </div>
              <div className="flex items-center gap-1.5">
                <button
                  onClick={() => setShowFilters(!showFilters)}
                  title={showFilters ? 'Hide filters' : 'Show filters'}
                  className={`inline-flex items-center gap-1 px-2 py-1 text-xs border rounded-sm ${showFilters ? 'bg-primary text-primary-foreground border-primary' : 'bg-card hover:bg-secondary border-border'}`}
                >
                  <SlidersHorizontal size={14} /> Filters
                </button>
                <button
                  onClick={toggleTags}
                  title={showTags ? 'Hide tags' : 'Show tags'}
                  aria-pressed={showTags}
                  className={`inline-flex items-center gap-1 px-2 py-1 text-xs border rounded-sm ${showTags ? 'bg-primary text-primary-foreground border-primary' : 'bg-card hover:bg-secondary border-border'}`}
                >
                  <Tags size={14} /> Tags
                </button>
              </div>
            </div>
            <div className="p-2 md:p-3">
              <ImageGrid
                images={images}
                isLoading={isLoading}
                error={error}
                onRetry={refetch}
                onDelete={isAdmin ? confirmDelete : undefined}
                onEdit={isAdminOrModerator ? (img) => { setEditingImage(img); setIsEditModalOpen(true); } : undefined}
                onSuggest={user && !isAdminOrModerator ? (img) => { setSuggestingImage(img); setIsSuggestModalOpen(true); } : undefined}
                onRemove={onRemoveFromAlbum}
                readOnly={readOnly}
                showTags={showTags}
                emptyState={emptyState || (
                  <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
                    <Search size={36} className="mb-3 opacity-30" />
                    <p className="font-medium">No posts found.</p>
                    <button
                      onClick={() => { setSearchParams(new URLSearchParams()); refetch(); }}
                      className="mt-4 px-3 py-1.5 bg-secondary border border-border text-foreground text-sm rounded-sm"
                    >
                      Clear filters
                    </button>
                  </div>
                )}
              />

              <Pagination
                currentPage={page}
                totalPages={totalPages}
                setPage={setPage}
                disabled={currentSort === ImageOrderBy.Random}
              />
            </div>
          </div>
        </div>
      </div>

      <ConfirmModal
        isOpen={isDeleteModalOpen}
        onClose={() => setIsDeleteModalOpen(false)}
        onConfirm={handleRealDelete}
        title="Delete Image"
        message="Permanently delete this image? This cannot be undone."
        confirmText="Delete"
        variant="destructive"
      />

      {editingImage && (
        <ImageModal
          isOpen={isEditModalOpen}
          onClose={() => { setIsEditModalOpen(false); setEditingImage(null); }}
          initialData={editingImage}
          onSubmit={handleSaveEdit}
        />
      )}

      {suggestingImage && (
        <ImageModal
          isOpen={isSuggestModalOpen}
          onClose={() => {
            setIsSuggestModalOpen(false);
            setSuggestingImage(null);
            queryClient.invalidateQueries({ queryKey: ['my-pending-image-edits'] });
          }}
          initialData={suggestingImage}
          onSubmit={() => {}}
          suggestContext={{ targetType: ReviewableContentType.Image, targetId: suggestingImage.id, current: suggestingImage }}
        />
      )}
    </div>
  );
};
