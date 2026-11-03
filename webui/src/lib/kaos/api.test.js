// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04/F-06): new file, Apache-2.0.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const calls = [];
vi.mock('../api', () => ({
    API_ENDPOINT: '/api/v1',
    defaultAPIHeaders: {},
    extractError: async () => 'refused',
    repositories: { create: vi.fn(async (r) => calls.push(['repo', r])), list: vi.fn() },
    objects: { upload: vi.fn(async (repo, branch, path) => calls.push(['upload', repo, branch, path])) },
    commits: { commit: vi.fn(async (repo, branch, msg) => calls.push(['commit', repo, branch, msg])) },
    branches: {
        get: vi.fn(async () => ({ id: 'main', commit_id: 'c0ffee' })),
        create: vi.fn(async (repo, name, src) => calls.push(['branch', repo, name, src])),
    },
    tags: {
        list: vi.fn(async () => ({ results: [{ id: 'v2' }, { id: 'v10' }, { id: 'vnext' }], pagination: {} })),
        create: vi.fn(async (repo, id, ref) => ({ id, commit_id: ref })),
    },
    pulls: { create: vi.fn(async (repo, p) => ({ id: 'pr1', ...p })) },
}));

import { createDataset, getSharedWithUs, getUIConfig, newStorageNamespace, proposeChange, releaseVersion } from './api';

beforeEach(() => {
    calls.length = 0;
    globalThis.fetch = vi.fn(async (url, init) => {
        calls.push(['fetch', url, init?.method || 'GET', init?.body ? JSON.parse(init.body) : undefined]);
        const files = [
            { path: 'data.yaml', content: 'nc: 1' },
            { path: 'images/train/.keep', content: '' },
        ];
        return { ok: true, status: url.endsWith('/metadata') ? 204 : 200, json: async () => ({ files }) };
    });
});

describe('releaseVersion', () => {
    it('tags main as the version after the highest vN', async () => {
        expect(await releaseVersion('dataset-cars')).toEqual({ id: 'v11', commit_id: 'c0ffee' });
    });
});

describe('createDataset', () => {
    it('with no layout creates the repository and labels it, with no render and no commit', async () => {
        await createDataset({
            name: 'dataset-notes',
            description: '',
            template: 'none',
            values: {},
            storagePrefix: 'gs://b/lake/',
        });
        expect(calls.map((c) => c[0] + ':' + (c[0] === 'fetch' ? c[2] + ' ' + c[1] : c.slice(1, 2).join(' ')))).toEqual(
            ['repo:[object Object]', 'fetch:POST /api/v1/repositories/dataset-notes/metadata'],
        );
        expect(calls[1][3]).toEqual({ metadata: { 'kaos.kind': 'dataset' } });
    });
    it('renders, creates under the prefix, commits as one, then labels', async () => {
        const steps = [];
        await createDataset(
            {
                name: 'dataset-cars',
                description: ' Cars ',
                template: 'yolo',
                values: { classes: ['car'] },
                storagePrefix: 'gs://b/lake/',
            },
            (s) => steps.push(s),
        );
        expect(calls.map((c) => c[0] + ':' + (c[0] === 'fetch' ? c[2] + ' ' + c[1] : c.slice(1).join(' ')))).toEqual([
            'fetch:POST /kaos/v1/dataset-templates/yolo/render',
            'repo:[object Object]',
            'upload:dataset-cars main data.yaml',
            'upload:dataset-cars main images/train/.keep',
            'commit:dataset-cars main Create dataset-cars from the yolo layout',
            'fetch:POST /api/v1/repositories/dataset-cars/metadata',
        ]);
        expect(calls[1][1]).toEqual({
            name: 'dataset-cars',
            storage_namespace: expect.stringMatching(/^gs:\/\/b\/lake\/dataset-cars-[0-9a-f]{8}$/),
            default_branch: 'main',
        });
        expect(calls[5][3]).toEqual({
            metadata: { 'kaos.kind': 'dataset', 'kaos.template': 'yolo', 'kaos.description': 'Cars' },
        });
        expect(steps[0]).toBe('Rendering the layout');
    });
});

describe('newStorageNamespace', () => {
    it('is new for every creation, so a deleted name can be created again', () => {
        const a = newStorageNamespace('gs://b/lake/', 'dataset-cars');
        const b = newStorageNamespace('gs://b/lake', 'dataset-cars');
        expect(a).toMatch(/^gs:\/\/b\/lake\/dataset-cars-[0-9a-f]{8}$/);
        expect(b).toMatch(/^gs:\/\/b\/lake\/dataset-cars-[0-9a-f]{8}$/);
        expect(a).not.toBe(b);
    });
});

describe('proposeChange', () => {
    it('changes main only through a pull request from a draft branch', async () => {
        const pr = await proposeChange('dataset-cars', { 'data.yaml': 'nc: 2' }, 'Edit classes');
        expect(calls[0][0]).toBe('branch');
        expect(calls[0][3]).toBe('main');
        const branch = calls[0][2];
        expect(calls[1]).toEqual(['upload', 'dataset-cars', branch, 'data.yaml']);
        expect(calls[2]).toEqual(['commit', 'dataset-cars', branch, 'Edit classes']);
        expect(pr).toMatchObject({ source_branch: branch, destination_branch: 'main', title: 'Edit classes' });
    });
});

describe('getUIConfig', () => {
    it('reads the settings once for all badges, and again after a failure', async () => {
        let fail = true;
        globalThis.fetch = vi.fn(async () => {
            if (fail) return { ok: false, status: 503, json: async () => ({}) };
            return { ok: true, status: 200, json: async () => ({ console_url: '', legacy_repository: 'proj' }) };
        });
        await expect(getUIConfig()).rejects.toBeTruthy();
        fail = false;
        const [a, b] = await Promise.all([getUIConfig(), getUIConfig()]);
        expect(a.legacy_repository).toBe('proj');
        expect(b).toBe(a);
        expect(globalThis.fetch).toHaveBeenCalledTimes(2);
    });
});

describe('getSharedWithUs', () => {
    const reply = (status, contentType, body) =>
        vi.fn(async () => ({
            ok: status >= 200 && status < 300,
            status,
            statusText: String(status),
            headers: new Headers(contentType ? { 'Content-Type': contentType } : {}),
            json: async () => body,
        }));
    const nothing = { project: '', datasets: [], notes: [] };

    it('reads the shares and notes', async () => {
        const body = {
            project: 'vision',
            datasets: [{ name: 'cars/dataset-roads', owner: 'cars', versions: [] }],
            notes: [{ owner: 'old', note: 'too old' }],
        };
        globalThis.fetch = reply(200, 'application/json', body);
        expect(await getSharedWithUs()).toEqual(body);
        expect(globalThis.fetch.mock.calls[0][0]).toBe('/kaos/v1/shared-with-us');
    });
    it('reads an older lakeFS (404, or the web UI page) as nothing shared', async () => {
        globalThis.fetch = reply(404, 'text/plain', null);
        expect(await getSharedWithUs()).toEqual(nothing);
        globalThis.fetch = reply(200, 'text/html; charset=utf-8', null);
        expect(await getSharedWithUs()).toEqual(nothing);
        globalThis.fetch = reply(200, '', null);
        expect(await getSharedWithUs()).toEqual(nothing);
    });
    it('reads missing or null lists as empty', async () => {
        globalThis.fetch = reply(200, 'application/json', { project: 'vision', datasets: null });
        expect(await getSharedWithUs()).toEqual({ project: 'vision', datasets: [], notes: [] });
    });
    it('throws on any other failure', async () => {
        globalThis.fetch = reply(401, 'application/json', { message: 'no' });
        await expect(getSharedWithUs()).rejects.toThrow('refused');
    });
});
