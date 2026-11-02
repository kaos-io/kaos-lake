// KAOS (kaos-lake, PRD novelcore/kaos#1283): new file, Apache-2.0.
//
// An outputs repository shown as its app's Model: what it is, links to the console's Models area and
// to MLflow, the versions registered in MLflow (model-<name>-v<N> tags) and the latest training runs.
import React, { useEffect, useState } from 'react';
import Badge from 'react-bootstrap/Badge';
import Table from 'react-bootstrap/Table';

import { Link } from '../components/nav';
import { useUIConfig } from './consoleLink';
import {
    DASH,
    consoleModelsUrl,
    datasetOf,
    filesHref,
    loadModel,
    mlflowModelsUrl,
    runRow,
    tagsByCommit,
} from './model';
import { commitHref, datasetHref } from './pipeline';
import { PURPOSE_COLOURS } from './purpose';

const External = ({ href, className = 'ms-3', children }) => (
    <a className={className} href={href} target="_blank" rel="noreferrer">
        {children}
    </a>
);

/** A run's dataset: a link to the dataset's page for a dataset-* ref, else the ref as text. */
export const DatasetRef = ({ value }) => {
    if (!value) return DASH;
    const dataset = datasetOf(value);
    if (!dataset) return <code>{value}</code>;
    return <Link href={datasetHref(dataset.repo)}>{`${dataset.repo}@${dataset.version}`}</Link>;
};

const RunLinks = ({ repoId, row }) => (
    <>
        <Link href={filesHref(repoId, row.id, row.path)}>Files</Link>
        {row.mlflowRun && (
            <External className="ms-2" href={row.mlflowRun}>
                MLflow run
            </External>
        )}
    </>
);

/** "Model of <app>", what it holds, and the console's and MLflow's model pages when configured. */
export const ModelHeader = ({ app, orphaned, consoleUrl, mlflowUrl }) => {
    const consoleModels = consoleModelsUrl(consoleUrl);
    const mlflowModels = mlflowModelsUrl(mlflowUrl);
    return (
        <div>
            <strong>{app ? `Model of ${app}` : 'Model of an ML app'}</strong>. Its checkpoints, trained weights and
            exports, written only by the app&apos;s pipeline runs under{' '}
            <code>&lt;kind&gt;/&lt;experiment&gt;/&lt;run&gt;/</code>. A version registered in MLflow is tagged{' '}
            <code>model-&lt;name&gt;-v&lt;N&gt;</code>.
            {orphaned && ' No app declares this repository any more; it is kept.'}
            {consoleModels && <External href={consoleModels}>Models in the KAOS console</External>}
            {mlflowModels && <External href={mlflowModels}>Open in MLflow</External>}
        </div>
    );
};

const Versions = ({ repoId, versions, mlflowUrl }) => (
    <>
        <div className="small fw-semibold mt-2">Registered versions</div>
        <Table size="sm" className="small mb-1">
            <thead>
                <tr>
                    <th>Version</th>
                    <th>Commit</th>
                    <th>Experiment</th>
                    <th>Run</th>
                    <th>Dataset</th>
                    <th />
                </tr>
            </thead>
            <tbody>
                {versions.map((v) => {
                    const row = runRow(v.commit || { id: v.commit_id }, mlflowUrl);
                    return (
                        <tr key={v.id}>
                            <td>
                                <code>{v.id}</code>
                            </td>
                            <td>
                                <Link href={commitHref(repoId, row.id)}>
                                    <code>{row.shortId}</code>
                                </Link>
                            </td>
                            <td>{row.experiment}</td>
                            <td>{row.run}</td>
                            <td>
                                <DatasetRef value={row.datasetRef} />
                            </td>
                            <td>
                                <RunLinks repoId={repoId} row={row} />
                            </td>
                        </tr>
                    );
                })}
            </tbody>
        </Table>
    </>
);

const Runs = ({ repoId, runs, registered, mlflowUrl }) => (
    <>
        <div className="small fw-semibold mt-2">Training runs</div>
        <Table size="sm" className="small mb-0">
            <thead>
                <tr>
                    <th>Date</th>
                    <th>Kind</th>
                    <th>Experiment</th>
                    <th>Run</th>
                    <th>Dataset</th>
                    <th />
                </tr>
            </thead>
            <tbody>
                {runs.map((c) => {
                    const row = runRow(c, mlflowUrl);
                    return (
                        <tr key={row.id}>
                            <td>
                                <Link href={commitHref(repoId, row.id)}>{row.date}</Link>
                            </td>
                            <td>{row.kind}</td>
                            <td>{row.experiment}</td>
                            <td>
                                {row.run}
                                {(registered[row.id] || []).map((tag) => (
                                    <Badge
                                        key={tag}
                                        pill
                                        bg=""
                                        className="ms-1"
                                        style={{ backgroundColor: PURPOSE_COLOURS.outputs, color: '#fff' }}
                                    >
                                        Registered as {tag}
                                    </Badge>
                                ))}
                            </td>
                            <td>
                                <DatasetRef value={row.datasetRef} />
                            </td>
                            <td>
                                <RunLinks repoId={repoId} row={row} />
                            </td>
                        </tr>
                    );
                })}
            </tbody>
        </Table>
    </>
);

/** The Model panel of an outputs repository. */
export const ModelPanel = ({ repoId, labels }) => {
    const config = useUIConfig();
    const [model, setModel] = useState(null);
    const [error, setError] = useState(null);
    useEffect(() => {
        let live = true;
        setModel(null);
        setError(null);
        loadModel(repoId)
            .then((m) => live && setModel(m))
            .catch((e) => live && setError(e));
        return () => {
            live = false;
        };
    }, [repoId]);
    const mlflowUrl = config.mlflow_url || '';
    return (
        <>
            <ModelHeader
                app={labels['kaos.app']}
                orphaned={labels['kaos.orphaned'] === 'true'}
                consoleUrl={config.console_url}
                mlflowUrl={mlflowUrl}
            />
            {error && <div className="small text-danger">{error.message}</div>}
            {!error && model === null && <div className="small text-muted">Reading the model…</div>}
            {model && model.versions.length === 0 && model.runs.length === 0 && (
                <div className="small text-muted">No training run has written to this model yet.</div>
            )}
            {model && model.versions.length > 0 && (
                <Versions repoId={repoId} versions={model.versions} mlflowUrl={mlflowUrl} />
            )}
            {model && model.runs.length > 0 && (
                <Runs
                    repoId={repoId}
                    runs={model.runs}
                    registered={tagsByCommit(model.versions)}
                    mlflowUrl={mlflowUrl}
                />
            )}
        </>
    );
};
