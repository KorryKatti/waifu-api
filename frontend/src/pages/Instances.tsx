import { useState, useEffect } from 'react';
import api from '../services/api';
import { MetaTags } from '../hooks/useMetaTags';
import { getEnv } from '../utils/env';
import { Server, CheckCircle2, XCircle, HelpCircle, Clock } from 'lucide-react';

// The instance index: other people's deployments of this software, and whether
// each one is serving its own images.
//
// # What is on a card, and what is not
//
// The components are shown separately and there is no score. A single number
// hides its own weights, and the person it is about is the one who would have to
// trust it — so an uptime claim can be checked against the operator's own
// experience of that instance, and "we could not tell" stays visibly different
// from "behind".
//
// Absence is rendered as "unknown" rather than as a dash or a zero. An instance
// that has never been checked has not been observed working, and an index that
// defaulted to 0% would be making a claim nobody made.

// Instance mirrors the public shape of GET /instances.
//
// Null means the value is genuinely absent rather than zero: an instance with no
// check history has no uptime figure, and one running a fork has no commit age
// because there is no shared history to measure against.
interface Instance {
    url: string;
    name?: string;
    uptime: number | null;
    schema: boolean | null;
    images: boolean | null;
    builtAgeDays: number | null;
    upstream: boolean | null;
    commitAgeDays: number | null;
    version?: string;
    lastCheckedAt: string | null;
}

interface InstanceList {
    instances: Instance[];
}

// Signal renders one named component.
//
// The three states are deliberate and all three are reachable: true, false, and
// absent. Collapsing "absent" into either of the others is how a list starts
// making claims it cannot support.
function Signal({
    label,
    value,
    ok,
    failed,
    unknownHint,
}: {
    label: string;
    value: boolean | null | undefined;
    ok: string;
    failed: string;
    unknownHint?: string;
}) {
    const unknown = value === null || value === undefined;

    return (
        <span
            className={`inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs font-medium ${
                unknown
                    ? 'bg-muted text-muted-foreground'
                    : value
                        ? 'bg-green-500/10 text-green-600 dark:text-green-400'
                        : 'bg-red-500/10 text-red-600 dark:text-red-400'
            }`}
            title={unknown ? unknownHint ?? 'not measured' : value ? ok : failed}
        >
            {unknown ? (
                <HelpCircle size={12} />
            ) : value ? (
                <CheckCircle2 size={12} />
            ) : (
                <XCircle size={12} />
            )}
            {label}
        </span>
    );
}

// checkAge renders how long ago the signals were observed.
//
// Staleness is the point of showing this at all: the prober runs daily, so a
// list without it would present last week's figures as current.
function checkAge(checkedAt: string | null): string {
    if (!checkedAt) {
        return 'never checked';
    }

    const then = new Date(checkedAt).getTime();
    if (Number.isNaN(then)) {
        return 'checked at an unparseable time';
    }

    const hours = Math.floor((Date.now() - then) / 3_600_000);
    if (hours < 1) return 'checked within the hour';
    if (hours < 48) return `checked ${hours}h ago`;

    return `checked ${Math.floor(hours / 24)}d ago`;
}

// RegisterHint is the only place the page tells someone how to get on it.
//
// It says anyone can run the command, because anyone can: the bot carries the
// credential, not the person, so there is no role to be given and nothing to ask
// permission for. An earlier version of this said someone would run it for you,
// which described a restriction the command does not have.
//
// It still says the bot is not always online, because that is the thing that
// actually wastes people's time - somebody runs the command, nothing answers,
// and they conclude the feature is broken.
const RegisterHint = () => {
    const discordServerUrl = getEnv('VITE_DISCORD_SERVER_URL');

    return (
        <span>
            Run <code>/register</code> in the{' '}
            {discordServerUrl ? (
                <a
                    href={discordServerUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-primary hover:underline"
                >
                    Discord server
                </a>
            ) : (
                'Discord server'
            )}{' '}
            with your instance URL &mdash; anyone can. The bot is only up when the
            operator has it running, so it may not answer straight away.
        </span>
    );
};

const Instances = () => {
    const [instances, setInstances] = useState<Instance[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        api.get<InstanceList>('/instances')
            .then(res => setInstances(res.data.instances ?? []))
            .catch(err => {
                // The list lives in the same service as the API, so an outage
                // takes both away. Saying so is more useful than an empty table
                // that reads as "nobody has registered".
                setError('The instance list is unavailable right now.');
                console.error(err);
            })
            .finally(() => setLoading(false));
    }, []);

    return (
        <div className="container mx-auto px-4 py-12 max-w-5xl">
            <MetaTags
                title="Instances"
                description="Other deployments of the waifu API, and whether each one is serving its own images."
            />

            <div className="text-center mb-10">
                <h1 className="text-4xl font-black mb-4 bg-gradient-to-r from-primary to-purple-600 bg-clip-text text-transparent">
                    Instances
                </h1>
                <p className="text-lg text-muted-foreground max-w-2xl mx-auto">
                    Anyone can run their own copy of this API. These are the ones people have registered.
                    Each one is checked once a day, and every signal below is published separately
                    rather than combined into a score.
                </p>
            </div>

            {loading && (
                <p className="text-center text-muted-foreground py-12">Loading instances…</p>
            )}

            {!loading && error && (
                <div className="text-center py-12">
                    <p className="text-muted-foreground">{error}</p>
                    <p className="text-sm text-muted-foreground mt-2">
                        This list is served by the same service as the API, so an outage takes both away.
                    </p>
                </div>
            )}

            {!loading && !error && instances.length === 0 && (
                <div className="text-center py-12">
                    <Server size={40} className="mx-auto mb-4 text-muted-foreground" />
                    <p className="text-muted-foreground">No instances have been registered yet.</p>
                    <p className="text-sm text-muted-foreground mt-2">
                        Running one? <RegisterHint />
                    </p>
                </div>
            )}

            <div className="grid gap-4">
                {instances.map(instance => (
                    <div
                        key={instance.url}
                        className="bg-card border border-border rounded-2xl p-6 hover:border-primary/50 transition-colors"
                    >
                        <div className="flex flex-wrap items-baseline justify-between gap-2 mb-4">
                            <div className="min-w-0">
                                <h2 className="text-lg font-bold truncate">
                                    {instance.name || instance.url}
                                </h2>
                                {instance.name && (
                                    <a
                                        href={instance.url}
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        className="text-sm text-primary hover:underline break-all"
                                    >
                                        {instance.url}
                                    </a>
                                )}
                            </div>
                            {instance.version && (
                                <span className="text-xs text-muted-foreground font-mono">
                                    v{instance.version}
                                </span>
                            )}
                        </div>

                        <div className="flex flex-wrap gap-2">
                            <Signal
                                label={instance.uptime === null
                                    ? 'uptime unknown'
                                    : `uptime ${(instance.uptime * 100).toFixed(1)}%`}
                                value={instance.uptime === null ? null : instance.uptime >= 0.8}
                                ok="passed most daily checks over the last month"
                                failed="failed most daily checks over the last month"
                                unknownHint="no checks have been recorded yet"
                            />
                            <Signal
                                label="images"
                                value={instance.images}
                                ok="served an image over its own URL"
                                failed="did not serve an image over its own URL"
                                unknownHint="not checked yet"
                            />
                            <Signal
                                label="catalog"
                                value={instance.schema}
                                ok="returned the expected response shape"
                                failed="returned a response a client could not read"
                                unknownHint="not checked yet"
                            />
                            <Signal
                                label={instance.builtAgeDays === null
                                    ? 'build age unknown'
                                    : `built ${instance.builtAgeDays}d ago`}
                                value={instance.builtAgeDays === null ? null : instance.builtAgeDays <= 180}
                                ok="deployed within the last six months"
                                failed="not redeployed in over six months"
                                unknownHint="the instance reported no usable build timestamp"
                            />
                        </div>

                        <div className="mt-4 pt-4 border-t border-border flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground">
                            <span className="inline-flex items-center gap-1">
                                <Clock size={12} />
                                {checkAge(instance.lastCheckedAt)}
                            </span>
                            {instance.upstream === true && (
                                <span title="This instance reported a commit that exists in this repository's history">
                                    running this repository&rsquo;s code
                                    {instance.commitAgeDays !== null && `, ${instance.commitAgeDays}d old`}
                                </span>
                            )}
                            {instance.upstream === null && instance.lastCheckedAt !== null && (
                                <span title="Its commit did not resolve in this repository's history, which is expected for a fork or a reimplementation">
                                    commit not recognised — expected for a fork
                                </span>
                            )}
                        </div>
                    </div>
                ))}
            </div>

            <div className="mt-10 text-center text-sm text-muted-foreground space-y-2">
                <p>
                    Partial instances are welcome. Nothing here is scored, and an instance that serves one image
                    is exactly as valid as one that serves ten thousand.
                </p>
                <p>
                    Running your own? <RegisterHint />
                </p>
            </div>
        </div>
    );
};

export default Instances;