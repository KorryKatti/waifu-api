import { useState, useEffect } from 'react';
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from 'lucide-react';

interface PaginationProps {
    currentPage: number;
    totalPages: number;
    setPage: (page: number) => void;
    disabled?: boolean;
    pageSize?: number;
    setPageSize?: (size: number) => void;
    scrollTargetId?: string;
}

const Pagination = ({ currentPage, totalPages, setPage, disabled = false, pageSize, setPageSize, scrollTargetId }: PaginationProps) => {
    const [inputVal, setInputVal] = useState(currentPage.toString());
    const [pageSizeInput, setPageSizeInput] = useState(pageSize?.toString() ?? '');

    // Sync input value when page changes externally
    useEffect(() => {
        setInputVal(currentPage.toString());
    }, [currentPage]);

    // Sync page size input when pageSize changes externally
    useEffect(() => {
        if (pageSize !== undefined) {
            setPageSizeInput(pageSize.toString());
        }
    }, [pageSize]);

    const changePage = (page: number) => {
        setPage(page);
        const target = scrollTargetId ? document.getElementById(scrollTargetId) : null;
        if (target) {
            target.scrollIntoView({ behavior: 'instant' as ScrollBehavior });
        } else {
            window.scrollTo({ top: 0, behavior: 'instant' as ScrollBehavior });
        }
    };

    // Hide if only 1 page and no page size selector
    if (totalPages <= 1 && !setPageSize) return null;

    if (disabled) {
        return (
            <div className="flex justify-center py-4 text-muted-foreground text-sm">
                Pagination disabled
            </div>
        );
    }

    const handleFirst = () => changePage(1);
    const handlePrev = () => changePage(Math.max(1, currentPage - 1));
    const handleNext = () => changePage(Math.min(totalPages, currentPage + 1));
    const handleLast = () => changePage(totalPages);

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        const val = parseInt(inputVal);
        if (!isNaN(val) && val >= 1 && val <= totalPages) {
            changePage(val);
        } else {
            setInputVal(currentPage.toString());
        }
    };

    const handlePageSizeBlur = () => {
        if (!setPageSize || pageSize === undefined) return;
        const val = parseInt(pageSizeInput);
        if (!isNaN(val) && val >= 1) {
            setPageSize(val);
        } else {
            setPageSizeInput(pageSize.toString());
        }
    };

    const handlePageSizeKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter') {
            e.preventDefault();
            handlePageSizeBlur();
        }
    };

    const buttonClass = "px-2 py-1 border border-border bg-card hover:bg-secondary disabled:opacity-40 disabled:cursor-not-allowed text-foreground rounded-sm";

    return (
        <div className="flex flex-wrap justify-center items-center gap-1 py-4 select-none text-sm">
            {setPageSize && pageSize !== undefined && (
                <div className="flex items-center gap-1.5 mr-2">
                    <input
                        type="text"
                        value={pageSizeInput}
                        onChange={(e) => setPageSizeInput(e.target.value)}
                        onBlur={handlePageSizeBlur}
                        onKeyDown={handlePageSizeKeyDown}
                        className="w-12 py-1 text-center bg-secondary border border-border focus:border-primary rounded-sm outline-none text-foreground"
                    />
                    <span className="text-muted-foreground">/ page</span>
                </div>
            )}

            {totalPages > 1 && (
                <>
                    <button onClick={handleFirst} disabled={currentPage === 1} className={buttonClass} title="First Page"><ChevronsLeft size={16} /></button>
                    <button onClick={handlePrev} disabled={currentPage === 1} className={buttonClass} title="Previous Page"><ChevronLeft size={16} /></button>
                    <form onSubmit={handleSubmit} className="flex items-center gap-1 mx-1">
                        <input
                            type="text"
                            value={inputVal}
                            onChange={(e) => setInputVal(e.target.value)}
                            className="w-12 py-1 text-center font-semibold bg-secondary border border-border focus:border-primary rounded-sm outline-none text-foreground"
                        />
                        <span className="text-foreground">/ {totalPages}</span>
                    </form>
                    <button onClick={handleNext} disabled={currentPage === totalPages} className={buttonClass} title="Next Page"><ChevronRight size={16} /></button>
                    <button onClick={handleLast} disabled={currentPage === totalPages} className={buttonClass} title="Last Page"><ChevronsRight size={16} /></button>
                </>
            )}
        </div>
    );
};

export default Pagination;
