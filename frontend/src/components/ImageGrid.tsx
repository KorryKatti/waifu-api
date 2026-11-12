import { ReactNode } from 'react';
import { ImageDto, Role } from '../types';
import ImageCard from './ImageCard';
import { RefreshCw } from 'lucide-react';
import { useAuth } from '../context/AuthContext';

export interface ImageGridProps {
  images: ImageDto[];
  isLoading: boolean;
  error?: any;
  onRetry?: () => void;
  onEdit?: (image: ImageDto) => void;
  onSuggest?: (image: ImageDto) => void;
  onDelete?: (id: number) => void;
  onRemove?: (id: number) => void;
  emptyState?: ReactNode;
  forceOverlay?: boolean;
  readOnly?: boolean;
  showTags?: boolean;
}

const ImageGrid = ({
  images, isLoading, error, onRetry,
  onEdit, onSuggest, onDelete, onRemove,
  emptyState, forceOverlay = false, readOnly = false,
  showTags = false
}: ImageGridProps) => {
  const { user } = useAuth();
  const isAdminOrModerator = user && (user.role === Role.Admin || user.role === Role.Moderator);
  const isAdmin = user && user.role === Role.Admin;

  if (isLoading) {
    return (
      <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-2">
        {[...Array(18)].map((_, i) => (
          <div key={i} className="bg-muted h-36 sm:h-40 border border-border animate-pulse" />
        ))}
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
        <p className="mb-3">Unable to load posts.</p>
        {onRetry && <button onClick={onRetry} className="inline-flex items-center gap-2 px-3 py-1.5 bg-primary text-primary-foreground rounded-sm text-sm"><RefreshCw size={14} /> Retry</button>}
      </div>
    );
  }

  if (images.length === 0) {
    return emptyState || (
      <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
        <p>No posts found.</p>
      </div>
    );
  }

  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-2 items-start">
      {images.map(img => (
        <ImageCard
          key={img.id}
          image={img}
          onDelete={isAdmin ? onDelete : undefined}
          onRemove={onRemove}
          onEdit={isAdminOrModerator ? onEdit : undefined}
          onSuggest={user && !isAdminOrModerator ? onSuggest : undefined}
          forceOverlay={forceOverlay}
          readOnly={readOnly}
          showTags={showTags}
        />
      ))}
    </div>
  );
};

export default ImageGrid;
