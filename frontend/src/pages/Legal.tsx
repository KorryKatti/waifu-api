import { ReactNode, useState } from 'react';
import { Copyright as CopyrightIcon, FileText, ShieldCheck } from 'lucide-react';
import { MetaTags } from '../hooks/useMetaTags';

// Plain-language legal pages for a volunteer-run instance.

const CONTACT_EMAIL = 'korrykatti@proton.me';
const LAST_UPDATED = 'November 9, 2026';

type LegalTab = 'terms' | 'privacy' | 'dmca';

const TABS: { value: LegalTab; label: string; icon: typeof FileText }[] = [
    { value: 'terms', label: 'Terms of Service', icon: FileText },
    { value: 'privacy', label: 'Privacy Policy', icon: ShieldCheck },
    { value: 'dmca', label: 'Copyright / DMCA', icon: CopyrightIcon },
];

function Section({ title, children }: { title: string; children: ReactNode }) {
    return (
        <section className="space-y-2">
            <h2 className="text-lg font-bold text-foreground">{title}</h2>
            <div className="space-y-2 text-sm leading-relaxed text-muted-foreground">{children}</div>
        </section>
    );
}

function List({ children }: { children: ReactNode }) {
    return <ul className="list-disc space-y-1 pl-5">{children}</ul>;
}

function Terms() {
    return (
        <div className="space-y-6">
            <Section title="1. Who runs this">
                <p>
                    The Service is run by an <strong>individual maintainer</strong>: a community fork kept alive after the
                    original Waifu.im project shut down. It is <strong>not affiliated with, endorsed by, or operated by the
                    original Waifu.im project or its maintainers.</strong>
                </p>
                <p>
                    Contact: <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.
                    No postal address is published. This is a volunteer-run service, and email is the contact method for
                    legal, privacy, and copyright matters.
                </p>
                <p>
                    These Terms cover this hosted Service only. The source code is MIT-licensed; anyone can self-host it,
                    and a self-hosted copy is a different service run by its own operator under that operator&apos;s own terms.
                </p>
            </Section>

            <Section title="2. Age restrictions">
                <List>
                    <li><strong>SFW use:</strong> 13 or older (or the minimum age of digital consent in your country).</li>
                    <li><strong>NSFW Content:</strong> 18 or older (or the age of majority where you live). Turning off NSFW filters means you are stating you are of legal age.</li>
                    <li>The &quot;I am 18+&quot; prompt is self-declaration, not age verification. You are responsible for keeping NSFW Content away from minors on your devices and accounts.</li>
                </List>
            </Section>

            <Section title="3. Accounts">
                <p>
                    Sign-in is via Discord OAuth, requesting only the <code className="font-mono">identify</code> scope: no email,
                    messages, or server membership. You are responsible for your account and for keeping your session token and
                    API keys confidential. API keys can be revoked anytime; sessions expire after 24 hours. Accounts may be
                    suspended or blacklisted for violating these Terms.
                </p>
            </Section>

            <Section title="4. Acceptable use">
                <p>You agree <strong>not</strong> to:</p>
                <List>
                    <li>upload anything unlawful, harmful, threatening, abusive, harassing, or otherwise objectionable;</li>
                    <li>depict minors in a pornographic, sexually suggestive, or violent manner, or create/possess/distribute CSAM in any form. That is absolutely prohibited; we will remove it and may report it to authorities;</li>
                    <li>infringe anyone&apos;s copyright or other rights;</li>
                    <li>try to access data or accounts you are not authorised for, scrape or bulk-download in a way that degrades the Service, evade rate limits or moderation, or interfere with the Service;</li>
                    <li>impersonate or mislead anyone.</li>
                </List>
                <p>We may remove Content and restrict accounts at our discretion, without notice.</p>
            </Section>

            <Section title="5. Your content">
                <List>
                    <li>You keep ownership of what you upload; we claim no ownership of images.</li>
                    <li>By uploading, you give us a free, non-exclusive licence to host, store, resize/re-encode, display, and serve it (including via CDN/API) to run the Service, and to remove it when these Terms or the law require.</li>
                    <li>You confirm you own the content or have all rights and permissions needed to share it.</li>
                    <li>Uploads may be reviewed by a person before publication and rejected submissions may be deleted.</li>
                </List>
            </Section>

            <Section title="6. Copyright and takedowns">
                <p>
                    We are a passive host for user uploads. To report infringing content, use the Report button on the image
                    page or see the <strong>Copyright / DMCA</strong> tab (contact <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>).
                    Repeat infringers may be removed.
                </p>
                <p>
                    Takedown notices are handled at the operator&apos;s sole discretion (see that tab for details). Raising claims in bad
                    faith to harass the operator or its users may result in those notices being ignored. As this is a volunteer
                    project, the operator may also pause or close uploads, or shut this instance down entirely and keep only the
                    Router, without notice.
                </p>
            </Section>

            <Section title="7. API and commercial use">
                <List>
                    <li>Free for <strong>non-commercial</strong> use, subject to fair use and rate limits. Abusive use may be throttled.</li>
                    <li><strong>Commercial use</strong> requires a visible attribution link to Waifu.im in the app using the Service; without it, commercial use breaches these Terms.</li>
                    <li>You are responsible for NSFW filtering and age restrictions in your own apps, and for respecting artists&apos; rights in what you display.</li>
                </List>
            </Section>

            <Section title="8. Third parties and community instances">
                <p>
                    The Service relies on Discord, cloud hosting, object storage, and CDN providers under their own terms. The{' '}
                    <strong>Router</strong> feature may send your browser directly to a third-party community instance, which serves
                    images itself: that instance is not run by us, and its operator&apos;s terms apply.
                </p>
            </Section>

            <Section title="9. Albums and favourites">
                <p>Provided for convenience only. There is no guarantee they will be kept. They may be modified or removed without notice.</p>
            </Section>

            <Section title="10. No warranties">
                <p>
                    The Service is provided <strong>&quot;AS IS&quot; / &quot;AS AVAILABLE&quot;</strong>, with no warranties of any kind. We do not promise it
                    will be uninterrupted, error-free, or that content is accurate or complete.
                </p>
            </Section>

            <Section title="11. Liability">
                <p>
                    To the maximum extent the law allows, we are not liable for indirect or consequential losses (including lost
                    data or profits) from using or being unable to use the Service. Total liability for any claim is capped at
                    US$100. Nothing here limits liability that cannot legally be limited.
                </p>
            </Section>

            <Section title="12. Indemnification">
                <p>If your uploads or misuse of the Service cause third-party claims or costs to us, you agree to cover them.</p>
            </Section>

            <Section title="13. Termination">
                <p>
                    You can stop using the Service anytime and ask for account deletion via <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.
                    We may suspend or terminate accounts anytime, with or without notice. Sections 5, 10, 11, 12, and 15 survive termination.
                </p>
            </Section>

            <Section title="14. Privacy">
                <p>Our <strong>Privacy Policy</strong> tab applies and is part of these Terms.</p>
            </Section>

            <Section title="15. Disputes">
                <p>
                    This is a free, volunteer-run service with no governing-law or court venue specified by choice. If something
                    goes wrong, contact us first and we&apos;ll try to sort it out informally, including by removing content,
                    restricting an account, or deleting data. Nothing here removes any rights you may have under the law where you live.
                </p>
            </Section>

            <Section title="16. Changes and general">
                <p>
                    We may update these Terms anytime, update the date above, and give notice through the Service for material
                    changes. Continued use means acceptance. If any provision is invalid, the rest continues in force. Failure to
                    enforce a provision is not a waiver. You may not assign your account without consent. We are not liable for
                    failures beyond our reasonable control (including hosting/CDN outages). These Terms and the Privacy Policy
                    are the entire agreement.
                </p>
            </Section>
        </div>
    );
}

function Privacy() {
    return (
        <div className="space-y-6">
            <Section title="1. What we collect">
                <p><strong>Via Discord sign-in</strong> (<code className="font-mono">identify</code> scope only): Discord ID, Discord username, avatar URL, account role, and blacklist status. We do not receive your email, messages, or server membership, and we never handle a password.</p>
                <p><strong>Content you submit:</strong> uploaded image files and the source URL you give us; tags, artists, album names/descriptions; NSFW flags; reports and edit proposals with their reasons; and derived metadata needed to run the Service (dimensions, file size, dominant colour, perceptual hash for duplicate detection).</p>
                <p><strong>API keys and usage:</strong> API key descriptions and creation/last-used/expiry dates: only a <strong>hash</strong> of each key is stored; the key itself is shown once and cannot be recovered. Plus simple per-account request counters (for rate limiting/abuse control) and aggregate daily statistics.</p>
                <p><strong>Your browser only (localStorage):</strong> <code className="font-mono">token</code> (24-hour session), <code className="font-mono">theme</code>, <code className="font-mono">nsfw-consent</code> (you confirmed 18+), <code className="font-mono">tag-view</code>, site-banner dismissal, and <code className="font-mono">auth.redirect</code> (post-login return page). <strong>We set no cookies of our own and run no analytics, advertising, or cross-site tracking.</strong> Clearing site data removes these and signs you out.</p>
                <p><strong>Automatic technical data:</strong> our hosting and CDN providers log IP addresses, user agents, requested URLs, and timestamps for security, debugging, and abuse prevention. Image delivery through the CDN logs requests the same way.</p>
            </Section>

            <Section title="2. Why we use it">
                <p>
                    To run the Service (sign-in, serving images/albums/API), moderate content, prevent abuse, keep the Service
                    secure, and comply with the law. We do <strong>not</strong> sell personal data, run ads, or send marketing email. We don&apos;t even
                    hold your email address.
                </p>
            </Section>

            <Section title="3. Who gets it">
                <p>
                    Only the processors needed to run the Service: <strong>Discord</strong> (sign-in, avatar CDN), <strong>PostgreSQL hosting</strong> (database),{' '}
                    <strong>S3-compatible storage</strong> (images), <strong>Cloudflare</strong> (CDN/images), and <strong>cloud hosting</strong> for the API and frontend. Each only
                    gets what its job needs.
                </p>
                <p>
                    <strong>Router / community instances:</strong> if you use Router, your browser talks directly to a third-party instance, which sees your
                    IP address. We don&apos;t proxy that traffic or control that operator&apos;s data handling.
                </p>
                <p>We disclose data only if legally required, to protect safety/rights, or if the Service is transferred.</p>
            </Section>

            <Section title="4. How long we keep it">
                <List>
                    <li>Account data: while your account exists</li>
                    <li>Uploaded images: until you, moderation, or a takedown removes them</li>
                    <li>API keys: until revoked or expired</li>
                    <li>Resolved review tasks: auto-deleted after 90 days</li>
                    <li>Daily aggregate stats: auto-deleted after 1 year</li>
                    <li>Server/CDN logs and backups: kept by our hosting providers under their own policies; we do not control those periods</li>
                </List>
            </Section>

            <Section title="5. Security">
                <p>
                    TLS in transit, hashed API keys, short-lived signed sessions, role-based access. Nothing is perfectly secure;
                    if a breach is likely to harm you, we&apos;ll notify affected users without unreasonable delay where the law needs it.
                </p>
            </Section>

            <Section title="6. Children">
                <p>
                    Not a service for children: 13+ for SFW, 18+ for NSFW. If a child has given us personal data, tell us at{' '}
                    <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a> and we&apos;ll delete it.
                </p>
            </Section>

            <Section title="7. Your rights">
                <p>
                    You can ask us to <strong>access, correct, delete, export, or restrict</strong> your personal data, or to delete your account: email{' '}
                    <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>. We may verify it&apos;s you via your Discord account and will
                    respond within 30 days. Some moderation records may be kept in anonymised form.
                </p>
                <p>
                    We&apos;re a small volunteer-run service, not a data business: we collect the minimum needed to run the site (listed in §1), we
                    don&apos;t sell data or run ads, and we don&apos;t want more of your data than that. Whatever rights the law where you live gives you over
                    your personal data, we will honour them on request by email. No forms, no portals.
                </p>
            </Section>

            <Section title="8. Changes and contact">
                <p>
                    Material changes will be announced through the Service and the date above updated. Privacy questions and
                    requests: <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.
                </p>
            </Section>
        </div>
    );
}

function Dmca() {
    return (
        <div className="space-y-6">
            <Section title="1. Sending a takedown notice">
                <p>
                    Email <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a> (or use the <strong>Report</strong> button on the
                    image page) with:
                </p>
                <List>
                    <li>Your name and a way to reach you (email is enough);</li>
                    <li>What work is being infringed (or a list, if several);</li>
                    <li>Where the infringing material is. The image URL or image ID is enough;</li>
                    <li>A statement that you believe in good faith the use is unauthorised;</li>
                    <li>A statement, under penalty of perjury, that the notice is accurate and you are authorised to act for the copyright owner;</li>
                    <li>Your signature (typing your name is fine).</li>
                </List>
                <p>Incomplete notices can&apos;t be acted on until what&apos;s missing is supplied.</p>
            </Section>

            <Section title="2. What happens next">
                <p>
                    If the notice looks valid, we remove or disable the content and tell the uploader. We may restrict the
                    uploader&apos;s account while it&apos;s assessed. Acting on a notice is not an admission of liability.
                </p>
                <p>
                    Notices are reviewed at the operator&apos;s sole discretion. We may ask for additional proof of ownership or authority before
                    acting, and we may decline or ignore notices that are incomplete, unverifiable, frivolous, or submitted in bad
                    faith. Sending a notice does not create an obligation to remove content, and repeatedly submitting false or
                    abusive notices may result in those notices being disregarded.
                </p>
            </Section>

            <Section title="3. If you think your content was removed by mistake">
                <p>
                    Send a counter-notice to <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a> with your name and contact details, what was removed, a perjury
                    statement that it was removed by mistake or misidentification, acceptance of service of process, and your
                    signature. We forward it to the original complainant; unless they tell us within <strong>10 to 14 business days</strong> they&apos;ve
                    sued to block restoration, we may put the content back.
                </p>
            </Section>

            <Section title="4. Repeat infringers and false notices">
                <p>
                    Accounts that repeatedly infringe may be terminated. Submitting a false takedown or counter-notice can make you
                    liable for the resulting damages. Only send notices for content you genuinely believe infringes your rights.
                </p>
            </Section>

            <Section title="5. Operator&apos;s discretion, including shutting things down">
                <p>
                    This Service is a volunteer community project, not a business. If copyright claims or takedown requests are used to
                    harass the operator or its users, or if handling them stops being viable, the operator may, at sole discretion
                    and without notice, pause or close uploads, remove user content features, or shut down this instance of the
                    fork entirely, keeping only the Router (which hosts no uploads and serves images directly from community
                    instances). No compensation is owed for any such change.
                </p>
            </Section>

            <Section title="6. Minors">
                <p>
                    Content depicting minors in a pornographic, sexually suggestive, or violent manner is banned outright. There is no notice
                    process needed. Report it via the Report button or <a className="text-primary underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>; it
                    will be removed immediately and may be reported to authorities.
                </p>
            </Section>
        </div>
    );
}

const Legal = () => {
    const [tab, setTab] = useState<LegalTab>('terms');

    return (
        <div className="container mx-auto px-4 py-12 max-w-4xl">
            <MetaTags
                title="Legal"
                description="Terms of Service, Privacy Policy, and Copyright / DMCA information for Waifu.im."
            />

            <div className="text-center mb-8">
                <h1 className="text-4xl font-black mb-4 bg-gradient-to-r from-primary to-purple-600 bg-clip-text text-transparent">
                    Legal
                </h1>
                <p className="text-lg text-muted-foreground max-w-2xl mx-auto">
                    Terms, privacy, and copyright info for Waifu.im, run by an individual maintainer, in plain language.
                </p>
                <p className="mt-2 text-xs text-muted-foreground">Last updated: {LAST_UPDATED}</p>
            </div>

            <div className="flex flex-wrap justify-center gap-2 mb-8" role="tablist" aria-label="Legal documents">
                {TABS.map(({ value, label, icon: Icon }) => (
                    <button
                        key={value}
                        type="button"
                        role="tab"
                        aria-selected={tab === value}
                        onClick={() => setTab(value)}
                        className={`inline-flex items-center gap-2 rounded-full px-4 py-2 text-sm font-bold transition-colors ${tab === value
                            ? 'bg-primary text-primary-foreground shadow-sm'
                            : 'border border-border bg-card text-muted-foreground hover:bg-secondary hover:text-foreground'}`}
                    >
                        <Icon size={16} />
                        {label}
                    </button>
                ))}
            </div>

            <div className="bg-card border border-border rounded-2xl p-6 md:p-8 shadow-sm">
                {tab === 'terms' && <Terms />}
                {tab === 'privacy' && <Privacy />}
                {tab === 'dmca' && <Dmca />}
            </div>

            <p className="mt-6 text-center text-xs text-muted-foreground">
                Questions about any of the above? Email{' '}
                <a className="underline" href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.
            </p>
        </div>
    );
};

export default Legal;
