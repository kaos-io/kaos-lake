// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-10): new file, Apache-2.0.
//
// What the MeluXina tab shows, as plain functions: the snapshot's alerts, its staged versions
// grouped by dataset with the tag each commit has, and the sizes and times people read.
import { defaultAPIHeaders, extractError } from '../api';

/** null or undefined (the snapshot sends null for unknown values). */
export const isNil = (v) => v === null || v === undefined;

export const MELUXINA_LOCATION = 'HPC (Slurm)';
/** Free space under this share of the limit raises the space alert (PRD 3.8). */
export const LOW_SPACE_FRACTION = 0.1;
/** A snapshot older than this is stale (PRD 3.8). */
export const STALE_AFTER_MS = 30 * 60 * 1000;

export const RETENTION_RULE =
    'Versions stay on the HPC system until new data needs the space. The least recently used version goes first. ' +
    'A version that a queued or running job needs is never removed.';

/**
 * The latest snapshot: { enabled, snapshot, skewMs }. enabled is false when this lakeFS does not
 * serve it: a 404, or the web UI's own page (an unknown path answers with index.html); skewMs is the
 * server's clock minus the browser's, so "stale" does not depend on the browser's clock being right.
 */
export const getMeluxina = async () => {
    const response = await fetch('/kaos/v1/meluxina', { headers: new Headers(defaultAPIHeaders) });
    const json = (response.headers.get('Content-Type') || '').includes('application/json');
    if (response.status === 404 || (response.ok && !json)) return { enabled: false, snapshot: null, skewMs: 0 };
    if (!response.ok) {
        throw new Error((await extractError(response)) || `${response.status} ${response.statusText}`);
    }
    const body = await response.json();
    const server = body.server_time ? Date.parse(body.server_time) : Date.now();
    return { enabled: true, snapshot: body.snapshot || null, skewMs: server - Date.now() };
};

/** "1.5 GiB", "340 MiB", "12 B". */
export const formatBytes = (bytes) => {
    if (!Number.isFinite(bytes) || bytes < 0) return '–';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    let value = bytes;
    let i = 0;
    while (value >= 1024 && i < units.length - 1) {
        value /= 1024;
        i += 1;
    }
    return `${i === 0 ? value : value.toFixed(value < 10 ? 2 : 1)} ${units[i]}`;
};

/** "just now", "5 minutes ago", "3 hours ago", "2 days ago". */
export const relativeTime = (iso, nowMs = Date.now()) => {
    const then = Date.parse(iso);
    if (!iso || Number.isNaN(then) || then <= 0) return 'never';
    const seconds = Math.max(0, Math.round((nowMs - then) / 1000));
    const unit = (n, name) => `${n} ${name}${n === 1 ? '' : 's'} ago`;
    if (seconds < 60) return 'just now';
    if (seconds < 3600) return unit(Math.floor(seconds / 60), 'minute');
    if (seconds < 86400) return unit(Math.floor(seconds / 3600), 'hour');
    return unit(Math.floor(seconds / 86400), 'day');
};

/** The exact time for a hover title; empty when there is none. */
export const exactTime = (iso) => {
    const t = Date.parse(iso);
    return !iso || Number.isNaN(t) || t <= 0
        ? ''
        : new Date(t)
              .toISOString()
              .replace('T', ' ')
              .replace(/\.\d+Z$/, ' UTC');
};

/** Used share of the limit, 0..100 (0 when there is no limit). */
export const usedPercent = (space) =>
    space && space.limit_bytes > 0 ? Math.min(100, Math.round((space.used_bytes / space.limit_bytes) * 100)) : 0;

/**
 * Alerts for a snapshot at nowMs (server time): low space, stale snapshot, last staging error.
 * Each is { id, text }.
 */
export const alertsFor = (snapshot, nowMs) => {
    if (!snapshot) return [];
    const alerts = [];
    const { space } = snapshot;
    if (
        space &&
        space.limit_bytes > 0 &&
        !isNil(space.free_bytes) &&
        space.free_bytes / space.limit_bytes < LOW_SPACE_FRACTION
    ) {
        const pct = Math.round((space.free_bytes / space.limit_bytes) * 100);
        alerts.push({
            id: 'space',
            text: `Space is low: ${formatBytes(space.free_bytes)} free of ${formatBytes(space.limit_bytes)} (${pct}%). Staging a new version removes the least recently used ones.`,
        });
    }
    // Staleness is about the last good data, not the last attempt: a failed run is not fresh data.
    const good = Date.parse(snapshot.last_success_at || '');
    if (Number.isNaN(good)) {
        alerts.push({ id: 'stale', text: 'No successful snapshot yet, so nothing below is known to be on the HPC system.' });
    } else if (nowMs - good > STALE_AFTER_MS) {
        alerts.push({
            id: 'stale',
            text: `This snapshot is stale: the last good data is from ${relativeTime(snapshot.last_success_at, nowMs)}. The stager reports every few minutes, so what is shown may no longer be on the HPC system.`,
        });
    }
    if (snapshot.last_error) alerts.push({ id: 'error', text: `Last staging error: ${snapshot.last_error}` });
    return alerts;
};

/** Tag names of the commit (a staged commit may be abbreviated), or [] when untagged. */
export const tagsOfCommit = (versions, commit) =>
    (versions || [])
        .filter((v) => v.commit_id && commit && (v.commit_id.startsWith(commit) || commit.startsWith(v.commit_id)))
        .map((v) => v.id);

/** Staged versions grouped by repository (name order), each group most recently used first. */
export const groupByDataset = (snapshot) => {
    const groups = new Map();
    for (const s of (snapshot && snapshot.staged) || []) {
        if (!groups.has(s.repo)) groups.set(s.repo, []);
        groups.get(s.repo).push(s);
    }
    const when = (s) => Date.parse(s.last_used || s.staged_at) || 0;
    return [...groups.keys()]
        .sort()
        .map((repo) => ({ repo, staged: groups.get(repo).sort((a, b) => when(b) - when(a)) }));
};
