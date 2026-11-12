import { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Plus, Minus, Clock } from 'lucide-react';

export type TagChipTone = 'neutral' | 'pending' | 'add' | 'remove';

const TONES: Record<TagChipTone, { cls: string; icon?: ReactNode }> = {
    neutral: { cls: 'bg-transparent booru-link' },
    pending: { cls: 'bg-amber-500/10 text-amber-600 border border-dashed border-amber-500/40 px-1 rounded-sm', icon: <Clock size={11} className="shrink-0" /> },
    add: { cls: 'bg-green-500/10 text-green-700 dark:text-green-400 px-1 rounded-sm', icon: <Plus size={12} className="shrink-0" /> },
    remove: { cls: 'bg-destructive/10 text-destructive px-1 rounded-sm', icon: <Minus size={12} className="shrink-0" /> },
};

interface TagChipProps {
    label: ReactNode;
    /** neutral = accepted/value, pending = awaiting review, add/remove = edit deltas. */
    tone?: TagChipTone;
    /** When set, the chip becomes a link to the entity. */
    href?: string;
    /** Overrides the tone's default leading icon (e.g. a tag/artist glyph for neutral chips). */
    icon?: ReactNode;
    /** Optional trailing icon, layered on top of the tone (e.g. a Clock on a proposed-add that is also pending). */
    trailingIcon?: ReactNode;
    title?: string;
}

/**
 * A small entity chip (tag or artist) used everywhere they appear: review diffs, the new-content preview and
 * the gallery tag strip. The `tone` selects the colour + default leading icon; `href` makes it a link.
 */
export const TagChip = ({ label, tone = 'neutral', href, icon, trailingIcon, title }: TagChipProps) => {
    const t = TONES[tone];
    const lead = icon !== undefined ? icon : t.icon;
    const base = `inline-flex items-center gap-1 py-0.5 text-xs ${t.cls} ${tone === 'neutral' ? '' : ''}`;
    const inner = <>{lead}{label}{trailingIcon}</>;
    return href
        ? <Link to={href} title={title} className={`${base} hover:underline`}>{inner}</Link>
        : <span title={title} className={`${base} ${tone === 'neutral' ? 'text-foreground' : ''}`}>{inner}</span>;
};

export default TagChip;
