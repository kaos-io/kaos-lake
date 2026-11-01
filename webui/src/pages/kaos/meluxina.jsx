// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-10): new file, Apache-2.0.
//
// The MeluXina tab of the Datasets page: per dataset the versions staged on MeluXina's Lustre with
// their times, files and size, the space against the quota, the retention rule and the alerts.
import React, { useEffect, useState } from 'react';
import Alert from 'react-bootstrap/Alert';
import Badge from 'react-bootstrap/Badge';
import Card from 'react-bootstrap/Card';
import ProgressBar from 'react-bootstrap/ProgressBar';
import Table from 'react-bootstrap/Table';

import { AlertError, Loading } from '../../lib/components/controls';
import { Link } from '../../lib/components/nav';
import { DATASET_PREFIX, listVersions } from '../../lib/kaos/api';
import {
    MELUXINA_LOCATION,
    RETENTION_RULE,
    alertsFor,
    exactTime,
    formatBytes,
    getMeluxina,
    isNil,
    groupByDataset,
    relativeTime,
    tagsOfCommit,
    usedPercent,
} from '../../lib/kaos/meluxina';
import { commitHref, datasetHref } from '../../lib/kaos/pipeline';

const count = (n) => (isNil(n) ? '–' : n.toLocaleString());

const Time = ({ iso, now }) => (
    <span title={exactTime(iso)}>{iso && !iso.startsWith('0001') ? relativeTime(iso, now) : 'never'}</span>
);

const SpaceCard = ({ snapshot }) => {
    const { space } = snapshot;
    const pct = usedPercent(space);
    return (
        <Card className="mb-3">
            <Card.Body>
                <Card.Title as="h6">Space on MeluXina</Card.Title>
                <ProgressBar
                    now={pct}
                    variant={
                        space.limit_bytes > 0 && !isNil(space.free_bytes) && space.free_bytes / space.limit_bytes < 0.1
                            ? 'danger'
                            : 'success'
                    }
                    label={`${pct}%`}
                />
                <div className="small mt-2">
                    {formatBytes(space.used_bytes)} used of{' '}
                    {isNil(space.limit_bytes) ? 'an unlimited or unreadable quota' : formatBytes(space.limit_bytes)} ·{' '}
                    <strong>
                        {isNil(space.free_bytes) ? 'free space unknown' : `${formatBytes(space.free_bytes)} free`}
                    </strong>{' '}
                    · {count(space.used_files)} of {isNil(space.limit_files) ? 'unlimited' : count(space.limit_files)}{' '}
                    files
                </div>
            </Card.Body>
        </Card>
    );
};

const RetentionCard = ({ snapshot }) => (
    <Card className="mb-3">
        <Card.Body>
            <Card.Title as="h6">When versions are removed</Card.Title>
            <div className="small">{RETENTION_RULE}</div>
            {snapshot.retention && (
                <div className="small text-muted mt-1">The stager reports: {snapshot.retention}</div>
            )}
        </Card.Body>
    </Card>
);

/** One dataset's staged versions; the dataset's tags name the commits. */
const DatasetStaged = ({ repo, staged, now }) => {
    const [versions, setVersions] = useState(null);
    const [error, setError] = useState(null);
    useEffect(() => {
        if (!repo.startsWith(DATASET_PREFIX)) {
            setVersions([]);
            return;
        }
        listVersions(repo)
            .then(setVersions)
            .catch((e) => {
                setVersions([]);
                setError(e);
            });
    }, [repo]);
    return (
        <Card className="mb-3">
            <Card.Body>
                <h6 className="mb-2">
                    {repo.startsWith(DATASET_PREFIX) ? <Link href={datasetHref(repo)}>{repo}</Link> : repo}
                    <Badge
                        pill
                        bg=""
                        className="ms-2"
                        style={{ backgroundColor: '#0b5394' }}
                        title="Where these copies are"
                    >
                        📍 {MELUXINA_LOCATION}
                    </Badge>
                </h6>
                {error && <AlertError error={error} onDismiss={() => setError(null)} />}
                <Table size="sm" responsive className="mb-0 align-middle">
                    <thead>
                        <tr>
                            <th>Version</th>
                            <th>Staged</th>
                            <th>Last used</th>
                            <th className="text-end">Files</th>
                            <th className="text-end">Size</th>
                            <th>Status</th>
                        </tr>
                    </thead>
                    <tbody>
                        {staged.map((s) => {
                            const tags = versions ? tagsOfCommit(versions, s.commit) : [];
                            return (
                                <tr key={s.commit}>
                                    <td>
                                        {versions === null ? (
                                            '…'
                                        ) : tags.length ? (
                                            <strong>{tags.join(', ')}</strong>
                                        ) : (
                                            <span className="text-muted">untagged</span>
                                        )}{' '}
                                        <Link href={commitHref(s.repo, s.commit)} title="Open the commit">
                                            <code>{s.commit.slice(0, 12)}</code>
                                        </Link>
                                    </td>
                                    <td>
                                        <Time iso={s.staged_at} now={now} />
                                    </td>
                                    <td>
                                        <Time iso={s.last_used} now={now} />
                                    </td>
                                    <td className="text-end">{count(s.files)}</td>
                                    <td className="text-end">{formatBytes(s.bytes)}</td>
                                    <td>
                                        {isNil(s.leased) ? (
                                            <span className="text-muted small" title="The job queue could not be read">
                                                unknown
                                            </span>
                                        ) : s.leased ? (
                                            <Badge bg="warning" text="dark" title="Never removed while a job needs it">
                                                In use by a queued/running job
                                            </Badge>
                                        ) : (
                                            <span className="text-muted small">idle</span>
                                        )}
                                    </td>
                                </tr>
                            );
                        })}
                    </tbody>
                </Table>
            </Card.Body>
        </Card>
    );
};

const MeluxinaTab = () => {
    const [state, setState] = useState(null);
    const [error, setError] = useState(null);
    useEffect(() => {
        getMeluxina().then(setState).catch(setError);
    }, []);
    if (error) return <AlertError error={error} />;
    if (!state) return <Loading />;
    if (!state.enabled) {
        return (
            <Card body>
                MeluXina staging is not set up for this project, so there is nothing to show here. Projects that train
                on MeluXina (the <code>ml_hpc</code> pipelines) get this tab filled in.
            </Card>
        );
    }
    const { snapshot, skewMs } = state;
    if (!snapshot) {
        return (
            <Card body>
                <p className="mb-1">No snapshot from MeluXina yet.</p>
                <p className="text-muted mb-0">
                    The stager reports what is staged every few minutes. If this stays empty, no pipeline of this
                    project has run on MeluXina yet; once one does, its datasets show up here.
                </p>
            </Card>
        );
    }
    const now = Date.now() + skewMs;
    const groups = groupByDataset(snapshot);
    return (
        <>
            {alertsFor(snapshot, now).map((a) => (
                <Alert key={a.id} variant={a.id === 'error' ? 'danger' : 'warning'}>
                    {a.text}
                </Alert>
            ))}
            <div className="small text-muted mb-2">
                {snapshot.last_success_at ? (
                    <>
                        Last good data from <Time iso={snapshot.last_success_at} now={now} /> (
                        {exactTime(snapshot.last_success_at)}).
                    </>
                ) : (
                    'No good data yet.'
                )}{' '}
                Last attempt <Time iso={snapshot.taken_at} now={now} />.
            </div>
            <div className="row">
                <div className="col-md-6">
                    <SpaceCard snapshot={snapshot} />
                </div>
                <div className="col-md-6">
                    <RetentionCard snapshot={snapshot} />
                </div>
            </div>
            {groups.length === 0 && (
                <Card body>
                    <p className="mb-1">Nothing is staged on MeluXina right now.</p>
                    <p className="text-muted mb-0">
                        A version is copied there the first time an HPC pipeline run uses it, and stays until the space
                        is needed.
                    </p>
                </Card>
            )}
            {groups.map((g) => (
                <DatasetStaged key={g.repo} repo={g.repo} staged={g.staged} now={now} />
            ))}
        </>
    );
};

export default MeluxinaTab;
