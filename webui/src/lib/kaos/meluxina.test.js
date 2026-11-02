// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-10): new file, Apache-2.0.
import { describe, expect, it, vi } from 'vitest';

vi.mock('../api', () => ({ API_ENDPOINT: '/api/v1', defaultAPIHeaders: {}, extractError: async () => 'refused' }));

import {
    LOW_SPACE_FRACTION,
    STALE_AFTER_MS,
    alertsFor,
    formatBytes,
    getMeluxina,
    groupByDataset,
    relativeTime,
    tagsOfCommit,
    usedPercent,
} from './meluxina';
import { canonicalTemplate, templateTitle } from './templates';
import { CLI_README_URL, pipelineRef, pushCommand, releaseCommand } from './pipeline';

const now = Date.parse('2026-11-01T12:00:00Z');
const snap = (over = {}) => ({
    taken_at: '2026-11-01T11:59:00Z',
    last_success_at: '2026-11-01T11:59:00Z',
    space: { used_bytes: 50, limit_bytes: 100, free_bytes: 50, used_files: 1, limit_files: 10 },
    staged: [],
    ...over,
});

describe('alertsFor', () => {
    it('is quiet for a fresh snapshot with room and no error', () => {
        expect(alertsFor(snap(), now)).toEqual([]);
    });
    it('warns when free space is under 10%', () => {
        expect(LOW_SPACE_FRACTION).toBe(0.1);
        const a = alertsFor(snap({ space: { ...snap().space, free_bytes: 9 } }), now);
        expect(a.map((x) => x.id)).toEqual(['space']);
        expect(alertsFor(snap({ space: { ...snap().space, free_bytes: 10 } }), now)).toEqual([]);
    });
    it('marks a snapshot older than 30 minutes stale', () => {
        expect(STALE_AFTER_MS).toBe(1800000);
        expect(alertsFor(snap({ last_success_at: '2026-11-01T11:29:00Z' }), now).map((x) => x.id)).toEqual(['stale']);
        expect(alertsFor(snap({ last_success_at: '2026-11-01T11:31:00Z' }), now)).toEqual([]);
    });
    it('judges staleness by the last good data, and keeps showing a failure after it', () => {
        const failed = snap({
            taken_at: '2026-11-01T11:59:50Z',
            last_success_at: '2026-11-01T11:00:00Z',
            last_error: 'ssh list failed: x',
        });
        expect(alertsFor(failed, now).map((x) => x.id)).toEqual(['stale', 'error']);
        expect(alertsFor(snap({ last_success_at: null, last_error: 'e' }), now).map((x) => x.id)).toEqual([
            'stale',
            'error',
        ]);
    });
    it('does not call unknown (null) space low', () => {
        const unknown = { used_bytes: null, limit_bytes: null, free_bytes: null, used_files: null, limit_files: null };
        expect(alertsFor(snap({ space: unknown }), now)).toEqual([]);
        expect(alertsFor(snap({ space: { ...unknown, limit_bytes: 100 } }), now)).toEqual([]);
    });
    it('shows the last staging error verbatim', () => {
        const a = alertsFor(snap({ last_error: 'quota exceeded on /scratch' }), now);
        expect(a).toEqual([{ id: 'error', text: 'Last staging error: quota exceeded on /scratch' }]);
    });
});

describe('staged versions', () => {
    it('maps a commit to its tags, abbreviated or not, else untagged', () => {
        const versions = [
            { id: 'v2', commit_id: 'bbbb2222' },
            { id: 'v1', commit_id: 'aaaa1111' },
        ];
        expect(tagsOfCommit(versions, 'aaaa1111')).toEqual(['v1']);
        expect(tagsOfCommit(versions, 'bbbb')).toEqual(['v2']);
        expect(tagsOfCommit(versions, 'cccc3333')).toEqual([]);
    });
    it('groups by dataset, most recently used first', () => {
        const staged = [
            { repo: 'dataset-b', commit: 'c1', staged_at: '2026-11-01T01:00:00Z' },
            { repo: 'dataset-a', commit: 'c2', staged_at: '2026-11-01T01:00:00Z', last_used: '2026-11-01T02:00:00Z' },
            { repo: 'dataset-a', commit: 'c3', staged_at: '2026-11-01T01:00:00Z', last_used: '2026-11-01T03:00:00Z' },
        ];
        const g = groupByDataset({ staged });
        expect(g.map((x) => x.repo)).toEqual(['dataset-a', 'dataset-b']);
        expect(g[0].staged.map((s) => s.commit)).toEqual(['c3', 'c2']);
    });
});

describe('formatting', () => {
    it('formats bytes, times and percentages', () => {
        expect(formatBytes(12)).toBe('12 B');
        expect(formatBytes(1536)).toBe('1.50 KiB');
        expect(formatBytes(5 * 1024 ** 3)).toBe('5.00 GiB');
        expect(relativeTime('2026-11-01T11:55:00Z', now)).toBe('5 minutes ago');
        expect(relativeTime('2026-11-01T11:59:50Z', now)).toBe('just now');
        expect(relativeTime('2026-10-30T12:00:00Z', now)).toBe('2 days ago');
        expect(relativeTime('', now)).toBe('never');
        expect(usedPercent(snap().space)).toBe(50);
        expect(usedPercent({ limit_bytes: 0, used_bytes: 5 })).toBe(0);
    });
});

describe('names and commands', () => {
    it('keeps the old template name working', () => {
        expect(canonicalTemplate('yolo')).toBe('ultralytics');
        expect(canonicalTemplate('ultralytics')).toBe('ultralytics');
        expect(templateTitle('yolo')).toBe('Ultralytics (YOLO object detection)');
    });
    it('gives the exact strings people copy', () => {
        expect(pipelineRef('dataset-cars', 'v3')).toBe('dataset = dataset-cars@v3');
        expect(pushCommand('dataset-cars')).toBe('kubecore dataset push <folder> dataset-cars -m "<message>"');
        expect(releaseCommand('dataset-cars')).toBe('kubecore dataset release dataset-cars');
        expect(CLI_README_URL).not.toMatch(/@[0-9a-f]{7,}/);
    });
});

describe('getMeluxina', () => {
    const answer = (status, contentType, body) => ({
        ok: status >= 200 && status < 300,
        status,
        statusText: '',
        headers: new Headers(contentType ? { 'Content-Type': contentType } : {}),
        json: async () => body,
    });
    it('is not enabled where lakeFS answers with its web page (the view is not served)', async () => {
        globalThis.fetch = vi.fn(async () => answer(200, 'text/html; charset=utf-8', null));
        expect(await getMeluxina()).toEqual({ enabled: false, snapshot: null, skewMs: 0 });
    });
    it('is not enabled on a 404', async () => {
        globalThis.fetch = vi.fn(async () => answer(404, 'text/plain', null));
        expect((await getMeluxina()).enabled).toBe(false);
    });
    it('reads the snapshot when the view answers JSON', async () => {
        globalThis.fetch = vi.fn(async () => answer(200, 'application/json', { snapshot: snap(), server_time: null }));
        const got = await getMeluxina();
        expect(got.enabled).toBe(true);
        expect(got.snapshot.space.limit_bytes).toBe(100);
    });
    it('still reports a real error', async () => {
        globalThis.fetch = vi.fn(async () => answer(500, 'application/json', null));
        await expect(getMeluxina()).rejects.toThrow('refused');
    });
});
