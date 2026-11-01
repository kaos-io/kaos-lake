// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-07): new file, Apache-2.0.
//
// A strip under a repository's header that says what the repository is for and where to go next:
// for a dataset, the pipeline string of its latest version, its pull requests and a way back to
// Datasets; for an ML app's outputs, the app and its model and run folders.
import React, { useEffect, useState } from 'react';
import Alert from 'react-bootstrap/Alert';
import Button from 'react-bootstrap/Button';

import { objects } from '../api';
import { ClipboardButton } from '../components/controls';
import { Link } from '../components/nav';
import { KIND, listVersions } from './api';
import { browseHref, datasetHref, pipelineRef, pullsHref } from './pipeline';
import { useRepoLabels } from './purpose';

const MAX_FOLDERS = 30;

const folderName = (prefix) => prefix.replace(/\/$/, '').split('/').pop();

const listFolders = async (repoId, prefix) => {
    const page = await objects.list(repoId, 'main', prefix, '', false, MAX_FOLDERS);
    return page.results.filter((r) => r.path_type === 'common_prefix').map((r) => r.path);
};

const folderHref = (repoId, path) => ({
    pathname: '/repositories/:repoId/objects',
    params: { repoId },
    query: { ref: 'main', path },
});

/** The outputs repository's model folders; opening one lists its run folders (<model>/<run>/). */
const OutputsRuns = ({ repoId }) => {
    const [models, setModels] = useState(null);
    const [open, setOpen] = useState('');
    const [runs, setRuns] = useState(null);
    const [error, setError] = useState(null);
    useEffect(() => {
        listFolders(repoId, '').then(setModels).catch(setError);
    }, [repoId]);
    useEffect(() => {
        if (!open) return;
        setRuns(null);
        listFolders(repoId, open).then(setRuns).catch(setError);
    }, [repoId, open]);
    if (error) return <div className="small text-danger">{error.message}</div>;
    if (models === null) return <div className="small text-muted">Reading folders…</div>;
    if (models.length === 0) {
        return (
            <div className="small text-muted">
                No results yet. A pipeline run writes its results here as <code>&lt;model&gt;/&lt;run&gt;/</code> when
                it finishes.
            </div>
        );
    }
    return (
        <div className="small">
            <span className="text-muted">Models: </span>
            {models.map((m) => (
                <Button
                    key={m}
                    size="sm"
                    variant={open === m ? 'secondary' : 'outline-secondary'}
                    className="me-1 py-0"
                    onClick={() => setOpen(open === m ? '' : m)}
                >
                    {folderName(m)}
                </Button>
            ))}
            {open && (
                <div className="mt-1">
                    <span className="text-muted">Runs of {folderName(open)}: </span>
                    {runs === null && 'Reading…'}
                    {runs && runs.length === 0 && <span className="text-muted">none</span>}
                    {runs &&
                        runs.map((r) => (
                            <Link key={r} className="me-2" href={folderHref(repoId, r)}>
                                {folderName(r)}
                            </Link>
                        ))}
                </div>
            )}
        </div>
    );
};

const DatasetStrip = ({ repoId }) => {
    const [latest, setLatest] = useState(null);
    useEffect(() => {
        listVersions(repoId)
            .then((v) => setLatest(v[0] || null))
            .catch(() => setLatest(null));
    }, [repoId]);
    return (
        <>
            <div>
                <strong>Dataset.</strong> Pipelines train on its released versions.{' '}
                <Link href={datasetHref(repoId)}>Versions and how to use them</Link>
                <Link className="ms-3" href={pullsHref(repoId)}>
                    Open pull requests
                </Link>
                <Link className="ms-3" href={{ pathname: '/datasets' }}>
                    ← All datasets
                </Link>
            </div>
            {latest && (
                <div className="small mt-1">
                    In a pipeline: <code className="user-select-all">{pipelineRef(repoId, latest.id)}</code>
                    <ClipboardButton
                        variant="link"
                        size="sm"
                        text={pipelineRef(repoId, latest.id)}
                        tooltip="Copy the pipeline string"
                    />
                    <Link href={browseHref(repoId, latest.id)}>Browse {latest.id}</Link>
                </div>
            )}
        </>
    );
};

/** Renders nothing for a repository that is neither a dataset nor an app's outputs. */
export const KaosRepoPanel = ({ repoId }) => {
    const labels = useRepoLabels(repoId);
    const kind = labels['kaos.kind'];
    if (kind !== KIND.dataset && kind !== KIND.outputs) return null;
    return (
        <Alert variant="light" className="border mx-3 mt-3 mb-0 py-2">
            {kind === KIND.dataset ? (
                <DatasetStrip repoId={repoId} />
            ) : (
                <>
                    <div>
                        <strong>
                            Outputs of {labels['kaos.app'] ? `the app ${labels['kaos.app']}` : 'an ML app'}.
                        </strong>{' '}
                        Only that app writes here: checkpoints and results, one <code>&lt;model&gt;/&lt;run&gt;/</code>{' '}
                        folder per run.
                        {labels['kaos.orphaned'] === 'true' && ' No app declares this repository any more; it is kept.'}
                    </div>
                    <OutputsRuns repoId={repoId} />
                </>
            )}
        </Alert>
    );
};
