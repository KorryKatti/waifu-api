import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Artist, Role, ReviewableContentType, ReviewStatus } from '../types';
import { useAuth } from '../context/AuthContext';
import { Plus, Edit2, Trash2, ExternalLink, Info, Link as LinkIcon, GitPullRequest, Clock } from 'lucide-react';
import ArtistModal, { ArtistFormData } from '../components/modals/ArtistModal';
import ConfirmModal from '../components/modals/ConfirmModal';
import Modal from '../components/Modal';
import SearchInput from '../components/SearchInput';
import Pagination from '../components/Pagination';
import { useResource } from '../hooks/useResource';
import { useAuthGuard } from '../hooks/useRequireAuth';
import { MetaTags } from '../hooks/useMetaTags';

const Artists = () => {
  const { user } = useAuth();
  const checkAuth = useAuthGuard();

  const {
    items: artists,
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
  } = useResource<Artist>('/artists');

  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [isEditOpen, setIsEditOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [suggestArtist, setSuggestArtist] = useState<Artist | null>(null);
  const [selectedArtist, setSelectedArtist] = useState<Artist | null>(null);
  const [infoArtist, setInfoArtist] = useState<Artist | null>(null);
  const [formData, setFormData] = useState<ArtistFormData>({ name: '', twitter: '', pixiv: '', patreon: '', deviantArt: '' });

  const handleOpenCreate = () => {
    if (!checkAuth('Log in to create artists.')) return;
    setFormData({ name: '', twitter: '', pixiv: '', patreon: '', deviantArt: '' });
    setIsCreateOpen(true);
  };

  const handleCreate = async (data: ArtistFormData) => {
    const isMod = user?.role === Role.Moderator || user?.role === Role.Admin;
    const success = await createItem(data, isMod ? 'Artist created' : 'Artist submitted for review');
    if (success) {
      setIsCreateOpen(false);
      setFormData({ name: '', twitter: '', pixiv: '', patreon: '', deviantArt: '' });
    }
  };

  const handleEdit = async (data: ArtistFormData) => {
    if (!selectedArtist) return;
    const success = await updateItem(selectedArtist.id, data, 'Artist updated');
    if (success) setIsEditOpen(false);
  };

  const handleDelete = async () => {
    if (!selectedArtist) return;
    const success = await deleteItem(selectedArtist.id, 'Artist deleted');
    if (success) setIsDeleteOpen(false);
  };

  const openEdit = (e: React.MouseEvent, artist: Artist) => {
    e.preventDefault();
    e.stopPropagation();
    setSelectedArtist(artist);
    setFormData({
      name: artist.name,
      twitter: artist.twitter,
      pixiv: artist.pixiv,
      patreon: artist.patreon,
      deviantArt: artist.deviantArt,
      reviewStatus: artist.reviewStatus,
      creatorId: artist.creatorId
    });
    setIsEditOpen(true);
  };

  const openDelete = (e: React.MouseEvent, artist: Artist) => {
    e.preventDefault();
    e.stopPropagation();
    setSelectedArtist(artist);
    setIsDeleteOpen(true);
  };

  const canManage = user && (user.role === Role.Admin || user.role === Role.Moderator);
  const isAdmin = user?.role === Role.Admin;
  const isReviewMode = !(canManage);

  const linkList = (artist: Artist) => {
    const links = [
      artist.twitter,
      artist.pixiv,
      artist.patreon,
      artist.deviantArt,
    ].filter(Boolean) as string[];
    if (links.length === 0) return <span className="text-muted-foreground">—</span>;
    return (
      <div className="flex flex-wrap gap-x-2 gap-y-0.5">
        {links.map((href, i) => (
          <a key={i} href={href} target="_blank" rel="noopener noreferrer" className="booru-link inline-flex items-center gap-1 text-xs">
            <LinkIcon size={11} /> {href.replace(/^https?:\/\//, '').slice(0, 28)}{href.replace(/^https?:\/\//, '').length > 28 ? '…' : ''}
          </a>
        ))}
      </div>
    );
  };

  return (
    <div className="container mx-auto p-3 md:p-4 max-w-6xl">
      <MetaTags title="Artists" description="Browse and manage artists." />

      <div className="booru-panel">
        <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-3 px-3 py-2 border-b border-border bg-secondary/30">
          <div>
            <h1 className="text-xl font-bold text-foreground">Artists</h1>
            <p className="text-xs text-muted-foreground">Browse and manage artists.</p>
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
                <th className="px-3 py-2 font-semibold">ID</th>
                <th className="px-3 py-2 font-semibold">Posts</th>
                <th className="px-3 py-2 font-semibold">Links</th>
                <th className="px-3 py-2 font-semibold text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                [...Array(8)].map((_, i) => (
                  <tr key={i} className="border-b border-border"><td colSpan={5} className="px-3 py-3"><div className="h-5 bg-muted animate-pulse" /></td></tr>
                ))
              ) : artists.length === 0 ? (
                <tr><td colSpan={5} className="px-3 py-8 text-center text-muted-foreground">No artists found.</td></tr>
              ) : (
                artists.map(artist => (
                  <tr key={artist.id} className="border-b border-border hover:bg-secondary/20">
                    <td className="px-3 py-2 align-top">
                      <div className="flex items-center gap-2 flex-wrap">
                        <Link to={`/gallery?includedArtists=${artist.id}`} className="booru-link tag-artist font-semibold">{artist.name}</Link>
                        {artist.reviewStatus === ReviewStatus.Pending && (
                          <span className="text-[11px] font-medium bg-amber-500/10 text-amber-600 border border-dashed border-amber-500/40 px-1.5 py-0.5 rounded-sm inline-flex items-center gap-1">
                            <Clock size={10} /> Pending
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-3 py-2 align-top text-muted-foreground font-mono text-xs">#{artist.id}</td>
                    <td className="px-3 py-2 align-top">{artist.imageCount?.toLocaleString() ?? 0}</td>
                    <td className="px-3 py-2 align-top">{linkList(artist)}</td>
                    <td className="px-3 py-2 align-top">
                      <div className="flex gap-1 justify-end">
                        <button onClick={() => setInfoArtist(artist)} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="Info"><Info size={14} /></button>
                        <Link to={`/gallery?includedArtists=${artist.id}`} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="View posts"><ExternalLink size={14} /></Link>
                        {canManage ? (
                          <>
                            <button onClick={(e) => openEdit(e, artist)} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="Edit"><Edit2 size={14} /></button>
                            {isAdmin && <button onClick={(e) => openDelete(e, artist)} className="p-1.5 border border-border bg-card hover:bg-destructive/20 text-destructive rounded-sm" title="Delete"><Trash2 size={14} /></button>}
                          </>
                        ) : user && (
                          <button onClick={(e) => { e.preventDefault(); e.stopPropagation(); setSuggestArtist(artist); }} className="p-1.5 border border-border bg-card hover:bg-secondary rounded-sm" title="Suggest edit"><GitPullRequest size={14} /></button>
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

      <ArtistModal isOpen={isCreateOpen} onClose={() => setIsCreateOpen(false)} onSubmit={handleCreate} title="New Artist" isReviewMode={isReviewMode} submitLabel="Create" />
      <ArtistModal isOpen={isEditOpen} onClose={() => setIsEditOpen(false)} onSubmit={handleEdit} initialData={selectedArtist ? { ...formData, id: selectedArtist.id } : formData} title="Edit Artist" submitLabel="Save" onDelete={isAdmin ? () => setIsDeleteOpen(true) : undefined} />
      {suggestArtist && (
        <ArtistModal
          isOpen={!!suggestArtist}
          onClose={() => setSuggestArtist(null)}
          onSubmit={() => {}}
          initialData={{ name: suggestArtist.name, twitter: suggestArtist.twitter, pixiv: suggestArtist.pixiv, patreon: suggestArtist.patreon, deviantArt: suggestArtist.deviantArt }}
          title="Suggest changes"
          suggestContext={{ targetType: ReviewableContentType.Artist, targetId: suggestArtist.id, current: suggestArtist }}
        />
      )}
      <ConfirmModal isOpen={isDeleteOpen} onClose={() => setIsDeleteOpen(false)} onConfirm={handleDelete} title="Delete Artist" message={<>Delete artist <strong>{selectedArtist?.name}</strong>?</>} zIndex={70} />

      <Modal isOpen={!!infoArtist} onClose={() => setInfoArtist(null)} title="Artist Details">
        {infoArtist && (
          <div className="space-y-3 text-sm">
            <div><h3 className="text-xs font-bold text-muted-foreground uppercase">Name</h3><p className="font-medium break-words">{infoArtist.name}</p></div>
            <div className="grid grid-cols-1 gap-2">
              {infoArtist.twitter && <div><h3 className="text-xs font-bold text-muted-foreground uppercase">Twitter</h3><a href={infoArtist.twitter} target="_blank" rel="noopener noreferrer" className="booru-link break-all">{infoArtist.twitter}</a></div>}
              {infoArtist.pixiv && <div><h3 className="text-xs font-bold text-muted-foreground uppercase">Pixiv</h3><a href={infoArtist.pixiv} target="_blank" rel="noopener noreferrer" className="booru-link break-all">{infoArtist.pixiv}</a></div>}
              {infoArtist.deviantArt && <div><h3 className="text-xs font-bold text-muted-foreground uppercase">DeviantArt</h3><a href={infoArtist.deviantArt} target="_blank" rel="noopener noreferrer" className="booru-link break-all">{infoArtist.deviantArt}</a></div>}
              {infoArtist.patreon && <div><h3 className="text-xs font-bold text-muted-foreground uppercase">Patreon</h3><a href={infoArtist.patreon} target="_blank" rel="noopener noreferrer" className="booru-link break-all">{infoArtist.patreon}</a></div>}
            </div>
            <div className="pt-3 border-t border-border flex justify-end">
              <Link to={`/gallery?includedArtists=${infoArtist.id}`} className="px-3 py-1.5 bg-primary text-primary-foreground rounded-sm font-semibold hover:opacity-90">View posts</Link>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
};

export default Artists;
