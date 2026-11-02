// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-07): new file, Apache-2.0.
//
// A strip under a repository's header that says what the repository is for and where to go next:
// for a dataset, the pipeline string of its latest version, its pull requests and a way back to
// Datasets; for an ML app's outputs, the app's Model (modelPanel.jsx).
import React, { useEffect, useState } from 'react';
import Alert from 'react-bootstrap/Alert';

import { ClipboardButton } from '../components/controls';
import { Link } from '../components/nav';
import { KIND, listVersions } from './api';
import { ModelPanel } from './modelPanel';
import { browseHref, datasetHref, pipelineRef, pullsHref } from './pipeline';
import { useRepoLabels } from './purpose';

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
            {kind === KIND.dataset ? <DatasetStrip repoId={repoId} /> : <ModelPanel repoId={repoId} labels={labels} />}
        </Alert>
    );
};
