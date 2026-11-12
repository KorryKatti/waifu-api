import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Tag, Role, ReviewableContentType, ReviewStatus } from '../types';
import { useAuth } from '../context/AuthContext';
import Modal from '../components/Modal';
import TagModal, { TagFormData } from '../components/modals/TagModal';
import { Plus, Edit2, Trash2, ExternalLink, Info, GitPullRequest, Clock } from 'lucide-react';
import SearchInput from '../components/SearchInput';
import Pagination from '../components/Pagination';
import { useResource } from '../hooks/useResource';
import { useAuthGuard } from '../hooks/useRequireAuth';
import { MetaTags } from '../hooks/useMetaTags';

const Tags = () => {
  const { user } = useAuth();
  const checkAuth = useAuthGuard();

  const {
    items: tags,
    loading,
    page,
    setPage,
    totalPages,
    search,
    setSearch,
    searchType,
    setSearchType,
    createItem,
    updateItem,
    deleteItem
  } = useResource<Tag>('/tags');

  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [isEditOpen, setIsEditOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [suggestTag, setSuggestTag] = useState<Tag | null>(null);

  const [selectedTag, setSelectedTag] = useState<Tag | null>(null);
  const [infoTag, setInfoTag] = useState<Tag | null>(null);
  const [formData, setFormData] = useState<TagFormData>({ name: '', slug: '', description: '' });

  const handleOpenCreate = () => {
    if (!checkAuth('Log in to create tags.')) return;
    setFormData({ name: '', slug: '', description: '' });
    setIsCreateOpen(true);
  };

  const handleCreate = async (data: TagFormData) => {
    const success = await createItem(data, 'Tag created');
    if (success) {
      setIsCreateOpen(false);
      setFormData({ name: '', slug: '', description: '' });
    }
  };

  const handleEdit = async (data: TagFormData) => {
    if (!selectedTag) return;
    const success = await updateItem(selectedTag.id, data, 'Tag updated');
    if (success) setIsEditOpen(false);
  };

  const handleDelete = async () => {
    if (!selectedTag) return;
    const success = await deleteItem(selectedTag.id, 'Tag deleted');
    if (success) setIsDeleteOpen(false);
  };

  const openEdit = (e: React.MouseEvent, tag: Tag) => {
    e.preventDefault();
    e.stopPropagation();
    setSelectedTag(tag);
    setFormData({ name: tag.name, slug: tag.slug, description: tag.description, reviewStatus: tag.reviewStatus, creatorId: tag.creatorId });
    setIsEditOpen(true);
  };

  const openDelete = (e: React.MouseEvent, tag: Tag) => {
    e.preventDefault();
    e.stopPropagation();
    setSelectedTag(tag);
    setIsDeleteOpen(true);
  };

  const isAdmin = user?.role === Role.Admin;
  const isModerator = user?.role === Role.Moderator;
  const canEdit = isAdmin || isModerator;
  const isReviewMode = !(canEdit);

  return (
    <div className="container mx-auto p-3 md:p-4 max-w-6xl">
      <MetaTags title="Tags" description="Browse and manage image tags." />

      <div className="booru-panel">
        <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-3 px-3 py-2 border-b border-border bg-secondary/30">
          <div>
            <h1 className="text-xl font-bold text-foreground">Tags</h1>
            <p className="text-xs text-muted-foreground">Browse and manage image tags.</p>
          </div>
          <div className="flex gap-2 w-full sm:w-auto">
            <div className="flex-1 sm:w-64">
              <SearchInput value={search} onChange={setSearch} searchType={searchType} onSearchTypeChange={setSearchType} />
            </div>
            <button onClick={handleOpenCreate} className="inline-flex items-center gap-1 px-3 py-2 bg-primary text-primary-foreground rounded-sm font-semibold text-sm whitespace-nowrap">
              <Plus size={16} /> New
            </button>
          </div>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-secondary/40 text-left">
              <tr className="border-b border-border">
                <th className="px-3 py-2 font-semibold">Name</th>
                <th className="px-3 py-2 font-semibold">Slug / ID</th>
                <th className="px-3 py-2 font-semibold">Posts</th>
                <th className="px-3 py-2 font-semibold">Description</th>
                <th className="px-3 py-2 font-semibold text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                [...Array(8)].map((_, i) => (
                  <tr key={i} className="border-b border-border"><td colSpan={5} className="px-3 py-3"><div className="h-5 bg-muted animate-pulse" /></td></tr>
                ))
              ) : tags.length === 0 ? (
                <tr><td colSpan={5} className="px-3 py-8 text-center text-muted-foreground">No tags found.</td></tr>
              ) : (
                tags.map(tag => (
                  <tr key={tag.id} className="border-b border-border hover:bg-secondary/20">
                    <td className="px-3 py-2 align-top">
                      <div className="flex items-center gap-2 flex-wrap">
                        <Link to={`/gallery?includedTags=${encodeURIComponent(tag.slug)}`} className="booru-link font-semibold">{tag.name}</Link>
                        {tag.reviewStatus === ReviewStatus.Pending && (
                          <span className="text-[11px] font-medium bg-amber-500/10 text-amber-600 border border-dashed border-amber-500/40 px-1.5 py-0.5 rounded-sm inline-flex items-center gap-1" title="Awaiting review">
                            <Clock size={10} /> Pending
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-3 py-2 align-top text-muted-foreground">
                      <code className="text-xs">{tag.slug}</code>
                      <span className="ml-2 font-mono text-xs">#{tag.id}</span>
                    </td>
                    <td className="px-3 py-2 align-top">{tag.imageCount?.toLocaleString() ?? 0}</td>
                    <td className="px-3 py-2 align-top text-muted-foreground max-w-md"><span className="line-clamp-2">{tag.description || '—'}</span></td>
                    <td className="px-3 py-2 align-top">
                      <div className="flex gap-1 justify-end">
                        <button onClick={() => setInfoTag(tag)} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="Info"><Info size={14} /></button>
                        <Link to={`/gallery?includedTags=${encodeURIComponent(tag.slug)}`} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="View posts"><ExternalLink size={14} /></Link>
                        {canEdit ? (
                          <>
                            <button onClick={(e) => openEdit(e, tag)} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="Edit"><Edit2 size={14} /></button>
                            {isAdmin && <button onClick={(e) => openDelete(e, tag)} className="p-1.5 border border-border bg-card hover:bg-destructive/20 text-destructive rounded-sm" title="Delete"><Trash2 size={14} /></button>}
                          </>
                        ) : user && (
                          <button onClick={(e) => { e.preventDefault(); e.stopPropagation(); setSuggestTag(tag); }} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="Suggest edit"><GitPullRequest size={14} /></button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        {!loading && <div className="border-t border-border px-2"><Pagination currentPage={page} totalPages={totalPages} setPage={setPage} /></div>}
      </div>

      <TagModal isOpen={isCreateOpen} onClose={() => setIsCreateOpen(false)} onSubmit={handleCreate} initialData={{ name: '', slug: '', description: '' }} title="New Tag" isReviewMode={isReviewMode} submitLabel="Create" />
      <TagModal isOpen={isEditOpen} onClose={() => setIsEditOpen(false)} onSubmit={handleEdit} initialData={selectedTag ? { ...formData, id: selectedTag.id } : formData} title="Edit Tag" submitLabel="Save" onDelete={isAdmin ? () => setIsDeleteOpen(true) : undefined} />
      {suggestTag && (
        <TagModal isOpen={!!suggestTag} onClose={() => setSuggestTag(null)} onSubmit={() => {}} initialData={{ name: suggestTag.name, slug: suggestTag.slug, description: suggestTag.description }} title="Suggest changes" suggestContext={{ targetType: ReviewableContentType.Tag, targetId: suggestTag.id, current: suggestTag }} />
      )}

      <Modal isOpen={isDeleteOpen} onClose={() => setIsDeleteOpen(false)} title="Delete Tag" zIndex={70}>
        <div className="space-y-4">
          <p className="text-muted-foreground">Delete tag <strong>{selectedTag?.name}</strong>?</p>
          <div className="flex gap-2">
            <button onClick={() => setIsDeleteOpen(false)} className="flex-1 py-2 bg-secondary rounded-sm font-semibold">Cancel</button>
            <button onClick={handleDelete} className="flex-1 py-2 bg-destructive text-destructive-foreground rounded-sm font-semibold">Delete</button>
          </div>
        </div>
      </Modal>

      <Modal isOpen={!!infoTag} onClose={() => setInfoTag(null)} title="Tag Details">
        {infoTag && (
          <div className="space-y-3 text-sm">
            <div><h3 className="text-xs font-bold text-muted-foreground uppercase">Name</h3><p className="font-medium break-words">{infoTag.name}</p></div>
            <div><h3 className="text-xs font-bold text-muted-foreground uppercase">Slug</h3><code className="bg-secondary px-1.5 py-0.5 rounded-sm break-all">{infoTag.slug}</code></div>
            <div><h3 className="text-xs font-bold text-muted-foreground uppercase">Description</h3><p className="whitespace-pre-wrap break-words">{infoTag.description || 'No description available.'}</p></div>
            <div className="pt-3 border-t border-border flex justify-end">
              <Link to={`/gallery?includedTags=${encodeURIComponent(infoTag.slug)}`} className="px-3 py-1.5 bg-primary text-primary-foreground rounded-sm font-semibold hover:opacity-90">View posts</Link>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
};

export default Tags;
