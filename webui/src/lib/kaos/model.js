// KAOS (kaos-lake, PRD novelcore/kaos#1283): new file, Apache-2.0.
//
// An ML app's outputs repository is the app's Model: its checkpoints, trained weights, exports and
// quantized models. Every step run merges one commit into main with metadata {app, experiment,
// run, kind, ...} under <kind>/<experiment>/<run>/; a version registered in MLflow is tagged
// model-<name>-v<N> on its training commit. Kept as plain functions so what a row shows and where
// each link goes are tested.
import dayjs from 'dayjs';

import { commits, tags } from '../api';
import { DATASET_PREFIX, firstParentLog } from './api';

export const MODEL_TAG_PREFIX = 'model-';
export const RUN_KINDS = ['checkpoints', 'quantization'];
export const MAX_RUNS = 20;
const LISTING_AMOUNT = 100;
export const DASH = '—';

const MODEL_TAG = /^model-(.+)-v(\d+)$/;
const DATASET_REF = new RegExp(`^(${DATASET_PREFIX}[^@\\s]+)@(\\S+)$`);

const trim = (url) => (url || '').replace(/\/+$/, '');

/** { name, version } of a registered model version's tag (model-<name>-v<N>), or null. */
export const parseModelTag = (id) => {
    const m = MODEL_TAG.exec(id || '');
    return m ? { name: m[1], version: Number(m[2]) } : null;
};

/** The registered model versions among tags: by model name, newest version first within each. */
export const modelVersions = (tagList) =>
    tagList
        .flatMap((t) => {
            const parsed = parseModelTag(t.id);
            return parsed ? [{ ...t, ...parsed }] : [];
        })
        .sort((a, b) => a.name.localeCompare(b.name) || b.version - a.version);

/** Commit id → the model tags that point at it. */
export const tagsByCommit = (versions) => {
    const out = {};
    for (const v of versions) (out[v.commit_id] = out[v.commit_id] || []).push(v.id);
    return out;
};

/** The commits of a log that a step run merged into main (kind checkpoints or quantization). */
export const runCommits = (log) => log.filter((c) => RUN_KINDS.includes(c.metadata?.kind));

/** The dataset a ref names, { repo, version } for `dataset-<x>@vN`, else null. */
export const datasetOf = (ref) => {
    const m = DATASET_REF.exec(ref || '');
    return m ? { repo: m[1], version: m[2] } : null;
};

/** The KAOS console's Models area (under /dashboard), for `project` when given; '' without a console. */
export const consoleModelsUrl = (consoleUrl, project = '') =>
    consoleUrl ? `${trim(consoleUrl)}/dashboard/models${project ? `?project=${encodeURIComponent(project)}` : ''}` : '';

/** MLflow's registered models, or '' without MLflow. */
export const mlflowModelsUrl = (mlflowUrl) => (mlflowUrl ? `${trim(mlflowUrl)}/#/models` : '');

/** One MLflow run, or '' unless both MLflow and the run id are known. */
export const mlflowRunUrl = (mlflowUrl, runId) =>
    mlflowUrl && runId ? `${trim(mlflowUrl)}/#/runs/${encodeURIComponent(runId)}` : '';

/** The repository's files at a commit, opened at path when given. */
export const filesHref = (repoId, commitId, path = '') => ({
    pathname: '/repositories/:repoId/objects',
    params: { repoId },
    query: path ? { ref: commitId, path } : { ref: commitId },
});

/** What a row shows for a run commit: missing metadata as a dash, the MLflow run only when known. */
export const runRow = (commit, mlflowUrl = '') => {
    const md = commit.metadata || {};
    const kind = md.kind || '';
    return {
        id: commit.id,
        shortId: commit.id.slice(0, 12),
        date: commit.creation_date ? dayjs.unix(commit.creation_date).format('YYYY-MM-DD HH:mm') : DASH,
        kind: kind || DASH,
        experiment: md.experiment || DASH,
        run: md.run || DASH,
        datasetRef: md.dataset_ref || '',
        path: md.experiment && md.run ? `${kind || 'checkpoints'}/${md.experiment}/${md.run}/` : '',
        mlflowRun: mlflowRunUrl(mlflowUrl, md.mlflow_run_id),
    };
};

/**
 * The model's registered versions (each with its tagged commit, null when unreadable) and its
 * latest training runs on main. Bounded: up to 100 model tags and the last 100 first-parent commits
 * of main, plus one read per tagged commit that is older than those.
 */
export const loadModel = async (repoId) => {
    const [tagPage, log] = await Promise.all([
        tags.list(repoId, MODEL_TAG_PREFIX, '', LISTING_AMOUNT),
        firstParentLog(repoId, 'main', LISTING_AMOUNT),
    ]);
    const versions = modelVersions(tagPage.results || []);
    const known = new Map((log.results || []).map((c) => [c.id, c]));
    const missing = [...new Set(versions.map((v) => v.commit_id).filter((id) => !known.has(id)))];
    const read = await Promise.all(missing.map((id) => commits.get(repoId, id).catch(() => null)));
    read.filter(Boolean).forEach((c) => known.set(c.id, c));
    return {
        versions: versions.map((v) => ({ ...v, commit: known.get(v.commit_id) || null })),
        runs: runCommits(log.results || []).slice(0, MAX_RUNS),
    };
};
