// KAOS (kaos-lake, PRD novelcore/kaos#1283): new file, Apache-2.0.
//
// The KAOS dataset calls of the web UI. Every write goes through the regular lakeFS API with the
// signed-in person's session (P-3); /kaos/v1/dataset-templates (the dataset layouts) only renders
// files and checks a dataset against a layout's declarative rules.
import {
    API_ENDPOINT,
    branches,
    commits,
    defaultAPIHeaders,
    extractError,
    objects,
    pulls,
    repositories,
    tags,
} from '../api';

import { canonicalTemplate } from './templates';

export const DATASET_PREFIX = 'dataset-';
export const KIND = { dataset: 'dataset', outputs: 'outputs' };

const request = async (url, init = {}) => {
    const response = await fetch(url, { headers: new Headers(defaultAPIHeaders), ...init });
    if (!response.ok) {
        throw new Error((await extractError(response)) || `${response.status} ${response.statusText}`);
    }
    return response.status === 204 ? null : response.json();
};

/** The repository's kaos.* labels (and any other metadata). */
export const repoMetadata = (repoId) => request(`${API_ENDPOINT}/repositories/${encodeURIComponent(repoId)}/metadata`);

export const setRepoMetadata = (repoId, metadata) =>
    request(`${API_ENDPOINT}/repositories/${encodeURIComponent(repoId)}/metadata`, {
        method: 'POST',
        body: JSON.stringify({ metadata }),
    });

/** The newest `amount` commits of `ref`, following only first parents: on an outputs repository's
 * main that is one merge commit per step run, without the commits made on the run's own branch. */
export const firstParentLog = (repoId, ref, amount) =>
    request(
        `${API_ENDPOINT}/repositories/${encodeURIComponent(repoId)}/refs/${encodeURIComponent(ref)}/commits?` +
            new URLSearchParams({ amount: String(amount), first_parent: 'true' }),
    );

/** KAOS settings of the UI: { console_url, mlflow_url, legacy_repository } (each empty when unset).
 * Read once per page load: every repository's badge needs it. A failed read is retried on the next call. */
let uiConfig = null;
export const getUIConfig = () => {
    if (!uiConfig) {
        uiConfig = request('/kaos/v1/ui').catch((e) => {
            uiConfig = null;
            throw e;
        });
    }
    return uiConfig;
};

/**
 * The datasets other projects of the organisation share with this one, for the Datasets page:
 * { project, datasets, notes } (pkg/kaos/sharing). A lakeFS without the endpoint (older than
 * kaos.20) answers 404 or the web UI's own page: that reads as nothing shared.
 */
export const getSharedWithUs = async () => {
    const response = await fetch('/kaos/v1/shared-with-us', { headers: new Headers(defaultAPIHeaders) });
    const json = (response.headers.get('Content-Type') || '').includes('application/json');
    if (response.status === 404 || (response.ok && !json)) return { project: '', datasets: [], notes: [] };
    if (!response.ok) {
        throw new Error((await extractError(response)) || `${response.status} ${response.statusText}`);
    }
    const body = await response.json();
    return {
        project: body?.project || '',
        datasets: Array.isArray(body?.datasets) ? body.datasets : [],
        notes: Array.isArray(body?.notes) ? body.notes : [],
    };
};

export const listTemplates = () => request('/kaos/v1/dataset-templates');

export const renderTemplate = (template, name, description, values) =>
    request(`/kaos/v1/dataset-templates/${encodeURIComponent(template)}/render`, {
        method: 'POST',
        body: JSON.stringify({ name, description, values }),
    });

/** The problems a layout's declarative rules find in repoId@ref (kaos-lake never runs a layout's hook). */
export const validateLayout = (template, repoId, ref) =>
    request(`/kaos/v1/dataset-templates/${encodeURIComponent(template)}/validate`, {
        method: 'POST',
        body: JSON.stringify({ repository: repoId, ref }),
    });

export const parseTemplate = (template, content) =>
    request(`/kaos/v1/dataset-templates/${encodeURIComponent(template)}/parse`, {
        method: 'POST',
        body: JSON.stringify({ content }),
    });

const uploadText = (repoId, branch, path, content) =>
    objects.upload(repoId, branch, path, new File([content], path.split('/').pop() || 'file'));

/** All repositories whose name starts with prefix, every page. */
export const listByPrefix = async (prefix) => {
    const out = [];
    let after = '';
    for (;;) {
        const page = await repositories.list(prefix, after);
        out.push(...page.results.filter((r) => r.id.startsWith(prefix)));
        if (!page.pagination?.has_more) return out;
        after = page.pagination.next_offset;
    }
};

/**
 * A storage namespace no repository has used: `<prefix>/<name>-<8 hex>`. lakeFS keeps a deleted repository's data
 * and its `_lakefs/dummy` marker, and refuses to create over them, so `<prefix>/<name>` could be used once only:
 * delete and recreate of the same name failed. Every creation picks a new one; nothing is ever deleted.
 */
export const newStorageNamespace = (prefix, name) => {
    const id = Array.from(crypto.getRandomValues(new Uint8Array(4)), (b) => b.toString(16).padStart(2, '0')).join('');
    return `${prefix.replace(/\/+$/, '')}/${name}-${id}`;
};

/**
 * Creates dataset `name` from the layout `template` as the signed-in person: the repository, one commit of the
 * rendered files on main, then its labels. onStep reports progress.
 */
/** The "no layout" choice, the default (owner, 2026-11-03): an empty dataset, like `kaosctl dataset create --layout none`. */
export const NO_LAYOUT = 'none';

export const createDataset = async (
    { name, description, template: asked, values, storagePrefix },
    onStep = () => {},
) => {
    const template = canonicalTemplate(asked); // new datasets carry the layout's current name
    if (template === NO_LAYOUT) {
        onStep('Creating the repository');
        await repositories.create({
            name,
            storage_namespace: newStorageNamespace(storagePrefix, name),
            default_branch: 'main',
        });
        onStep('Labelling');
        const metadata = { 'kaos.kind': KIND.dataset };
        if (description.trim()) metadata['kaos.description'] = description.trim();
        await setRepoMetadata(name, metadata);
        return;
    }
    onStep('Rendering the layout');
    const { files } = await renderTemplate(template, name, description, values);
    onStep('Creating the repository');
    await repositories.create({
        name,
        storage_namespace: newStorageNamespace(storagePrefix, name),
        default_branch: 'main',
    });
    for (const f of files) {
        onStep(`Writing ${f.path}`);
        await uploadText(name, 'main', f.path, f.content);
    }
    onStep('Committing');
    await commits.commit(name, 'main', `Create ${name} from the ${template} layout`, { 'kaos.template': template });
    onStep('Labelling');
    const metadata = { 'kaos.kind': KIND.dataset, 'kaos.template': template };
    if (description.trim()) metadata['kaos.description'] = description.trim();
    await setRepoMetadata(name, metadata);
};

/** The dataset's versions (tags v1, v2, …), newest first. */
export const listVersions = async (repoId) => {
    const out = [];
    let after = '';
    for (;;) {
        const page = await tags.list(repoId, 'v', after, 1000);
        out.push(...page.results.filter((t) => /^v\d+$/.test(t.id)));
        if (!page.pagination?.has_more) break;
        after = page.pagination.next_offset;
    }
    return out.sort((a, b) => Number(b.id.slice(1)) - Number(a.id.slice(1)));
};

/** Tags main's head as the next version (v1, v2, …) and returns the tag. */
export const releaseVersion = async (repoId) => {
    const versions = await listVersions(repoId);
    const next = `v${versions.length ? Number(versions[0].id.slice(1)) + 1 : 1}`;
    const head = await branches.get(repoId, 'main');
    return tags.create(repoId, next, head.commit_id);
};

/**
 * Proposes new file contents for main through a pull request (OQ-2): a draft branch, one commit,
 * and a pull request into main. Returns the pull request.
 */
export const proposeChange = async (repoId, changes, title) => {
    const branch = `edit-${Date.now()}`;
    await branches.create(repoId, branch, 'main');
    for (const [path, content] of Object.entries(changes)) {
        await uploadText(repoId, branch, path, content);
    }
    await commits.commit(repoId, branch, title);
    return pulls.create(repoId, { title, description: '', source_branch: branch, destination_branch: 'main' });
};
