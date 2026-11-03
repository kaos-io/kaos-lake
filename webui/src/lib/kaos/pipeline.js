// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-07): new file, Apache-2.0.
//
// How a dataset version is named in a pipeline, and the CLI commands around a dataset. Kept as
// plain functions so the exact strings people copy are tested.

/** The pipeline parameter that pins a dataset version, e.g. `dataset = dataset-cars@v3`. */
export const pipelineRef = (repoId, version) => `dataset = ${repoId}@${version}`;

/** The CLI command that proposes a folder's files for a dataset (opens a pull request). */
export const pushCommand = (repoId) => `kaosctl dataset push <folder> ${repoId} -m "<message>"`;

/** The CLI command that releases the next version of a dataset. */
export const releaseCommand = (repoId) => `kaosctl dataset release ${repoId}`;

/** Where the kaosctl CLI (kaos-lib) is documented: its command reference (no pinned version here). */
export const CLI_README_URL = 'https://storage.googleapis.com/kaos-dev-511509-kaos-lib/docs/generated/cli.html';

/** Where a dataset's data is browsed at a version (a tag) or at main. */
export const browseHref = (repoId, ref = 'main') => ({
    pathname: '/repositories/:repoId/objects',
    params: { repoId },
    query: { ref },
});

export const commitHref = (repoId, commitId) => ({
    pathname: '/repositories/:repoId/commits/:commitId',
    params: { repoId, commitId },
});

export const tagsHref = (repoId) => ({ pathname: '/repositories/:repoId/tags', params: { repoId } });

export const pullsHref = (repoId) => ({ pathname: '/repositories/:repoId/pulls', params: { repoId } });

export const datasetHref = (repoId, query) => ({
    pathname: '/datasets/:repoId',
    params: { repoId },
    ...(query ? { query } : {}),
});

/** Where a draft branch is made: a dataset's main takes changes only through merged pull requests. */
export const branchesHref = (repoId) => ({ pathname: '/repositories/:repoId/branches', params: { repoId } });
