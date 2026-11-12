import { Outlet, Link, useLocation } from 'react-router-dom';
import { useTheme } from '../../context/ThemeContext';
import { useAuth } from '../../context/AuthContext';
import {
  Sun, Moon, Menu, X, LogOut, Upload as UploadIcon,
  Home, Image as ImageIcon, Tag as TagIcon, ChevronDown,
  User as UserIcon, Library, Palette, Key, Users as UsersIcon, Flag, BarChart, Monitor, Book, Mail, Activity, HardDrive, Github, FileText, GitPullRequest, Megaphone, Server, Route as RouteIcon
} from 'lucide-react';
import { useState, useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import GlobalErrorHandler from '../GlobalErrorHandler';
import { Dropdown, DropdownItem, DropdownLabel, DropdownSeparator } from '../Dropdown';
import { getEnv } from '../../utils/env';
import { Role } from '../../types';
import { useNavBadges, invalidateNavBadges } from '../../hooks/useNavBadges';
import { usePermissions } from '../../hooks/useBanner';
import SiteBanner from '../SiteBanner';

const Layout = () => {
  const { theme, setTheme, resolvedTheme } = useTheme();
  const { user, logout } = useAuth();
  const location = useLocation();
  const [isMobileMenuOpen, setIsMobileMenuOpen] = useState(false);

  const { pendingCount, reportCount } = useNavBadges();
  const queryClient = useQueryClient();
  useEffect(() => { invalidateNavBadges(queryClient); }, [location.pathname, queryClient]);

  const navBadge = pendingCount > 0 ? (
    <span className="ml-1 px-1.5 py-0.5 rounded text-[10px] font-bold leading-none bg-primary text-primary-foreground">
      {pendingCount > 99 ? '99+' : pendingCount}
    </span>
  ) : null;

  const reportBadge = reportCount > 0 ? (
    <span className="ml-1 px-1.5 py-0.5 rounded text-[10px] font-bold leading-none bg-red-600 text-white">
      {reportCount > 99 ? '99+' : reportCount}
    </span>
  ) : null;

  const isActive = (path: string) => location.pathname === path;

  const handleLogout = () => {
    logout();
    window.location.href = '/';
  };

  const appTitle = getEnv('VITE_APP_TITLE') || 'Waifu API';
  const docsUrl = getEnv('VITE_DOCS_URL');
  const contactEmail = getEnv('VITE_CONTACT_EMAIL');
  const discordServerUrl = getEnv('VITE_DISCORD_SERVER_URL');
  const statusUrl = getEnv('VITE_STATUS_URL');
  const githubUrl = getEnv('VITE_GITHUB_URL');
  const tosUrl = getEnv('VITE_TOS_URL');
  const showContact = !!contactEmail || !!discordServerUrl;

  const navItems = [
    { name: 'Home', path: '/', icon: Home },
    { name: 'Posts', path: '/gallery', icon: ImageIcon },
    { name: 'Albums', path: '/albums', icon: Library },
    { name: 'Tags', path: '/tags', icon: TagIcon },
    { name: 'Artists', path: '/artists', icon: Palette },
  ];

  const isModOrAdmin = user && (user.role === Role.Moderator || user.role === Role.Admin);
  const isAdmin = user && user.role === Role.Admin;
  const { canManageBanner } = usePermissions();

  const linkClass = (active: boolean) =>
    `inline-flex items-center gap-1.5 px-2 py-1 text-sm border-b-2 transition-colors ${
      active
        ? 'border-primary text-foreground font-semibold'
        : 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'
    }`;

  return (
    <div className="min-h-screen bg-background text-foreground flex flex-col">
      <GlobalErrorHandler />

      <header className="sticky top-0 z-50 w-full border-b border-border bg-card">
        <div className="px-3 lg:px-4 h-12 flex items-center justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0">
            <button
              className="lg:hidden p-1.5 rounded hover:bg-secondary"
              onClick={() => setIsMobileMenuOpen(!isMobileMenuOpen)}
              aria-label="Menu"
            >
              <Menu size={20} />
            </button>

            <Link to="/" className="flex items-center gap-2 min-w-0">
              <img src="/favicon.png" alt="Logo" className="w-6 h-6 rounded-sm" />
              <span className="text-base font-bold tracking-tight truncate">{appTitle}</span>
            </Link>

            <nav className="hidden lg:flex items-center gap-1 ml-2">
              {navItems.map((item) => (
                <Link key={item.path} to={item.path} className={linkClass(isActive(item.path))}>
                  {item.name}
                </Link>
              ))}
              <Link to="/upload" className={linkClass(isActive('/upload'))}>
                <UploadIcon size={14} /> Upload
              </Link>
            </nav>
          </div>

          <div className="flex items-center gap-1 sm:gap-2">
            <Dropdown
              width="w-36"
              trigger={
                <button className="p-1.5 rounded hover:bg-secondary text-muted-foreground hover:text-foreground" title="Theme">
                  {resolvedTheme === 'light' ? <Sun size={18} /> : <Moon size={18} />}
                </button>
              }
            >
              <DropdownItem onClick={() => setTheme('light')} active={theme === 'light'} icon={<Sun size={16} />}>Light</DropdownItem>
              <DropdownItem onClick={() => setTheme('dark')} active={theme === 'dark'} icon={<Moon size={16} />}>Dark</DropdownItem>
              <DropdownItem onClick={() => setTheme('system')} active={theme === 'system'} icon={<Monitor size={16} />}>System</DropdownItem>
            </Dropdown>

            {user ? (
              <Dropdown
                width="w-64"
                padding="p-0"
                trigger={
                  <button className="flex items-center gap-2 pl-1 pr-2 py-1 rounded hover:bg-secondary border border-transparent hover:border-border max-w-[220px]">
                    {user.avatarUrl ? (
                      <img src={user.avatarUrl} alt={user.name} className="w-6 h-6 shrink-0 rounded object-cover border border-border" />
                    ) : (
                      <div className="w-6 h-6 shrink-0 rounded bg-secondary flex items-center justify-center text-muted-foreground border border-border">
                        <UserIcon size={14} />
                      </div>
                    )}
                    <span className="text-sm font-medium truncate max-w-[120px] hidden sm:block">{user.name}</span>
                    <ChevronDown size={14} className="text-muted-foreground shrink-0" />
                  </button>
                }
              >
                <div className="px-3 py-2 border-b border-border bg-secondary/30">
                  <p className="font-semibold truncate text-sm">{user.name}</p>
                  <p className="text-xs text-muted-foreground">{user.role === Role.Admin ? 'Administrator' : user.role === Role.Moderator ? 'Moderator' : 'Member'}</p>
                </div>
                <div className="p-1">
                  <DropdownLabel>Personal</DropdownLabel>
                  <Link to="/upload"><DropdownItem icon={<UploadIcon size={16} />}>Upload Image</DropdownItem></Link>
                  <Link to="/albums"><DropdownItem icon={<Library size={16} />}>My Albums</DropdownItem></Link>
                  <Link to="/api-keys"><DropdownItem icon={<Key size={16} />}>API Keys</DropdownItem></Link>
                  {!isModOrAdmin && (
                    <>
                      <Link to="/review"><DropdownItem icon={<GitPullRequest size={16} />}>My Submissions{navBadge}</DropdownItem></Link>
                      <Link to="/my-reports"><DropdownItem icon={<Flag size={16} />}>My Reports</DropdownItem></Link>
                    </>
                  )}
                  {(isModOrAdmin || canManageBanner) && (
                    <>
                      <DropdownSeparator />
                      <DropdownLabel>Administration</DropdownLabel>
                      {canManageBanner && (
                        <Link to="/site-banner"><DropdownItem icon={<Megaphone size={16} className="text-primary" />}>Site Banner</DropdownItem></Link>
                      )}
                      {isModOrAdmin && (
                        <>
                          <Link to="/review"><DropdownItem icon={<GitPullRequest size={16} className="text-orange-500" />}>Review{navBadge}</DropdownItem></Link>
                          <Link to="/reports"><DropdownItem icon={<Flag size={16} className="text-red-500" />}>Reports{reportBadge}</DropdownItem></Link>
                        </>
                      )}
                      {isAdmin && (
                        <>
                          <Link to="/users"><DropdownItem icon={<UsersIcon size={16} className="text-blue-500" />}>User Management</DropdownItem></Link>
                          <Link to="/stats"><DropdownItem icon={<BarChart size={16} className="text-purple-500" />}>Statistics</DropdownItem></Link>
                          <Link to="/storage-diff"><DropdownItem icon={<HardDrive size={16} className="text-emerald-500" />}>Storage Diff</DropdownItem></Link>
                        </>
                      )}
                    </>
                  )}
                  <DropdownSeparator />
                  <DropdownItem onClick={handleLogout} icon={<LogOut size={16} />} danger>Log out</DropdownItem>
                </div>
              </Dropdown>
            ) : (
              <Link
                to="/login"
                className="px-3 py-1.5 rounded bg-primary text-primary-foreground text-sm font-semibold hover:opacity-90"
              >
                Login
              </Link>
            )}
          </div>
        </div>

        {/* Secondary links bar (booru-like utility row) */}
        <div className="hidden sm:flex flex-wrap items-center gap-x-3 gap-y-1 px-3 lg:px-4 py-1 border-t border-border bg-secondary/30 text-xs">
          {docsUrl && <a href={docsUrl} target="_blank" rel="noopener noreferrer" className="booru-link inline-flex items-center gap-1"><Book size={12} /> Docs</a>}
          {githubUrl && <a href={githubUrl} target="_blank" rel="noopener noreferrer" className="booru-link inline-flex items-center gap-1"><Github size={12} /> GitHub</a>}
          {statusUrl && <a href={statusUrl} target="_blank" rel="noopener noreferrer" className="booru-link inline-flex items-center gap-1"><Activity size={12} /> Status</a>}
          <Link to="/instances" className="booru-link inline-flex items-center gap-1"><Server size={12} /> Instances</Link>
          <Link to="/router" className="booru-link inline-flex items-center gap-1"><RouteIcon size={12} /> Router</Link>
          <Link to="/legal" className="booru-link inline-flex items-center gap-1"><FileText size={12} /> Legal</Link>
          {showContact && <Link to="/contact" className="booru-link inline-flex items-center gap-1"><Mail size={12} /> Contact</Link>}
          {tosUrl && <a href={tosUrl} target="_blank" rel="noopener noreferrer" className="booru-link inline-flex items-center gap-1"><FileText size={12} /> Terms</a>}
        </div>
      </header>

      {isMobileMenuOpen && (
        <div className="fixed inset-0 z-[60] lg:hidden">
          <div className="absolute inset-0 bg-black/50" onClick={() => setIsMobileMenuOpen(false)} />
          <div className="absolute inset-y-0 left-0 w-full max-w-xs bg-card border-r border-border shadow-lg flex flex-col p-3">
            <div className="flex justify-between items-center mb-4 pb-2 border-b border-border">
              <span className="font-bold">Menu</span>
              <button onClick={() => setIsMobileMenuOpen(false)} className="p-1.5 hover:bg-secondary rounded"><X size={20} /></button>
            </div>
            <nav className="space-y-1 flex-1 overflow-y-auto text-sm">
              {navItems.map((item) => (
                <Link
                  key={item.path}
                  to={item.path}
                  onClick={() => setIsMobileMenuOpen(false)}
                  className={`flex items-center gap-2 px-2 py-2 rounded ${isActive(item.path) ? 'bg-secondary font-semibold' : 'hover:bg-secondary/60'}`}
                >
                  <item.icon size={16} /> {item.name}
                </Link>
              ))}
              <Link to="/upload" onClick={() => setIsMobileMenuOpen(false)} className={`flex items-center gap-2 px-2 py-2 rounded ${isActive('/upload') ? 'bg-secondary font-semibold' : 'hover:bg-secondary/60'}`}>
                <UploadIcon size={16} /> Upload
              </Link>
              <div className="my-2 border-t border-border" />
              {docsUrl && <a href={docsUrl} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><Book size={16} /> Documentation</a>}
              {githubUrl && <a href={githubUrl} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><Github size={16} /> GitHub</a>}
              {statusUrl && <a href={statusUrl} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><Activity size={16} /> Status</a>}
              <Link to="/instances" onClick={() => setIsMobileMenuOpen(false)} className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><Server size={16} /> Instances</Link>
              <Link to="/router" onClick={() => setIsMobileMenuOpen(false)} className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><RouteIcon size={16} /> Router</Link>
              <Link to="/legal" onClick={() => setIsMobileMenuOpen(false)} className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><FileText size={16} /> Legal</Link>
              {showContact && <Link to="/contact" onClick={() => setIsMobileMenuOpen(false)} className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><Mail size={16} /> Contact</Link>}
              {tosUrl && <a href={tosUrl} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 px-2 py-2 rounded hover:bg-secondary/60"><FileText size={16} /> Terms of Service</a>}
            </nav>
          </div>
        </div>
      )}

      <main className="flex-1 relative">
        <SiteBanner />
        <Outlet />
      </main>
    </div>
  );
};

export default Layout;
