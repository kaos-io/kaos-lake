// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-06/F-10/F-12): new file, Apache-2.0.
//
// The Datasets page: the project's dataset-* repositories with their purpose, description,
// layout, versions and how to use them in a pipeline, a Release version action, a layout check, and
// New dataset;
// below them the datasets other projects share with this one; and the MeluXina tab with what is
// staged on the HPC site.
import React, { useCallback, useEffect, useState } from 'react';
import Button from 'react-bootstrap/Button';
import Card from 'react-bootstrap/Card';
import Container from 'react-bootstrap/Container';
import Nav from 'react-bootstrap/Nav';
import { useParams, useSearchParams } from 'react-router-dom';

import { AlertError, ClipboardButton, Loading } from '../../lib/components/controls';
import { Link } from '../../lib/components/nav';
import { DATASET_PREFIX, listByPrefix, listVersions, releaseVersion, validateLayout } from '../../lib/kaos/api';
import {
    CLI_README_URL,
    browseHref,
    datasetHref,
    pullsHref,
    pushCommand,
    releaseCommand,
    branchesHref,
} from '../../lib/kaos/pipeline';
import { PipelineUsage } from '../../lib/kaos/pipelineUsage';
import { useConsoleHref, useUIConfig } from '../../lib/kaos/consoleLink';
import { PurposeBadge, useRepoLabels } from '../../lib/kaos/purpose';
import { canonicalTemplate, templateTitle } from '../../lib/kaos/templates';
import MeluxinaTab from './meluxina';
import SharedWithUs from './sharedWithUs';

/** Sharing is set in the KAOS console (PRD novelcore/kaos#1283 F-11); here it is only shown. */
const SharingNote = ({ labels }) => {
    const href = useConsoleHref('datasets');
    const shared = (labels['kaos.shared_with'] || '').trim() !== '';
    if (!href) return null;
    return (
        <div className="small text-muted mt-1">
            {shared ? 'Sharing is managed' : 'Share it with other projects'} in the{' '}
            <a href={href} target="_blank" rel="noreferrer">
                KAOS console
            </a>
            .
        </div>
    );
};

/** After New dataset: what to do next, in order. */
const NextSteps = ({ repoId, hasVersion }) => (
    <div className="mt-3 p-3 border border-success rounded">
        <h6 className="mb-2">Your dataset is created. Next:</h6>
        <ol className="mb-0">
            <li className="mb-1">
                <strong>Add your data.</strong> In the browser: create a branch on{' '}
                <Link href={branchesHref(repoId)}>the branches page</Link>, upload files to it and open a pull request
                (main changes only when a pull request is merged). Or from a terminal, with the{' '}
                <a href={CLI_README_URL} target="_blank" rel="noreferrer">
                    kaosctl CLI
                </a>{' '}
                (<code>kaosctl login</code> first; <code>kaosctl dataset validate &lt;folder&gt;</code> checks the
                layout): <code className="user-select-all">{pushCommand(repoId)}</code>
                <ClipboardButton variant="link" size="sm" text={pushCommand(repoId)} tooltip="Copy the command" />
                opens a pull request to merge.
            </li>
            <li className="mb-1">
                <strong>Check it</strong> in <Link href={browseHref(repoId)}>the file browser</Link>. Change the class
                names later with Edit on <code>data.yaml</code>; a change goes through a pull request.
            </li>
            <li>
                <strong>Release a version</strong> with the green button here{hasVersion ? ' (v1 exists already)' : ''},
                or <code>{releaseCommand(repoId)}</code>. Pipelines then offer it, and the box below shows the exact
                text to put in a pipeline.
            </li>
        </ol>
    </div>
);

const MAX_PROBLEMS_SHOWN = 20;

/** What a layout check of main found: kaos-lake's answer for the layout's declarative rules. */
export const LayoutCheck = ({ result }) => {
    const { problems, checked, hook, layout } = result;
    return (
        <div className={`small mt-2 p-2 border rounded ${problems.length ? 'border-warning' : 'border-success'}`}>
            {problems.length ? (
                <>
                    <strong>
                        {problems.length} problem{problems.length === 1 ? '' : 's'} on main for layout {layout}:
                    </strong>
                    <ul className="mb-1">
                        {problems.slice(0, MAX_PROBLEMS_SHOWN).map((p) => (
                            <li key={p}>
                                <code>{p}</code>
                            </li>
                        ))}
                    </ul>
                    {problems.length > MAX_PROBLEMS_SHOWN && (
                        <div>… and {problems.length - MAX_PROBLEMS_SHOWN} more</div>
                    )}
                </>
            ) : (
                <strong>Main follows layout {layout}&apos;s rules.</strong>
            )}
            <div className="text-muted">
                {checked.files} files looked at
                {checked.partial
                    ? `; ${checked.skipped} files too large or too many to read were not checked line by line`
                    : ''}
                . kaos-lake checks the layout&apos;s declarative rules only
                {hook ? (
                    <>
                        ; its hook <code>{hook}</code> runs in <code>kaosctl layout validate</code>
                    </>
                ) : (
                    ''
                )}
                .
            </div>
        </div>
    );
};

export const DatasetCard = ({ repo, detail = false, created = false }) => {
    const labels = useRepoLabels(repo.id);
    const [usage, setUsage] = useState(detail);
    const [versions, setVersions] = useState(null);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState(null);
    const [released, setReleased] = useState('');
    const [check, setCheck] = useState(null);
    const layout = canonicalTemplate(labels['kaos.template']);
    const runCheck = async () => {
        setCheck('running');
        setError(null);
        try {
            setCheck(await validateLayout(layout, repo.id, 'main'));
        } catch (e) {
            setCheck(null);
            setError(e);
        }
    };
    const load = useCallback(() => {
        listVersions(repo.id)
            .then(setVersions)
            .catch((e) => {
                setVersions([]);
                setError(e);
            });
    }, [repo.id]);
    useEffect(load, [load]);
    const release = async () => {
        setBusy(true);
        setError(null);
        try {
            const tag = await releaseVersion(repo.id);
            setReleased(tag.id);
            setUsage(true);
            load();
        } catch (e) {
            setError(e);
        } finally {
            setBusy(false);
        }
    };
    const latest = versions && versions.length ? versions[0] : null;
    return (
        <Card className="mb-3">
            <Card.Body>
                <div className="d-flex justify-content-between align-items-start flex-wrap gap-2">
                    <div>
                        <h5 className="mb-1">
                            <Link href={datasetHref(repo.id)}>{repo.id}</Link>
                            <PurposeBadge labels={labels} repoId={repo.id} />
                        </h5>
                        {labels['kaos.description'] && <div className="text-muted">{labels['kaos.description']}</div>}
                        <div className="small text-muted mt-1">
                            {labels['kaos.template'] ? `Layout: ${templateTitle(labels['kaos.template'])} · ` : ''}
                            {versions === null
                                ? 'Reading versions…'
                                : latest
                                  ? `Latest version: ${latest.id} (${latest.commit_id.slice(0, 12)})`
                                  : 'No version released yet'}
                        </div>
                        <div className="small mt-1">
                            <Link href={browseHref(repo.id)}>Browse data</Link>
                            <Link className="ms-3" href={pullsHref(repo.id)}>
                                Open pull requests
                            </Link>
                        </div>
                        <SharingNote labels={labels} />
                    </div>
                    <div className="d-flex gap-2">
                        {layout && layout !== 'raw' && (
                            <Button
                                size="sm"
                                variant="outline-secondary"
                                disabled={check === 'running'}
                                onClick={runCheck}
                                title="Check main against the layout's rules"
                            >
                                {check === 'running' ? 'Checking…' : 'Check layout'}
                            </Button>
                        )}
                        <Button size="sm" variant="outline-secondary" onClick={() => setUsage(!usage)}>
                            How to use in a pipeline
                        </Button>
                        <Button
                            size="sm"
                            variant="success"
                            disabled={busy}
                            onClick={release}
                            title="Tag main's head as the next version; pipelines can then pick it"
                        >
                            {busy ? 'Releasing…' : 'Release version'}
                        </Button>
                    </div>
                </div>
                {created && <NextSteps repoId={repo.id} hasVersion={!!latest} />}
                {released && (
                    <div className="small text-success mt-2">
                        Released {released}. Use it in a pipeline as{' '}
                        <code>
                            dataset = {repo.id}@{released}
                        </code>
                        .
                    </div>
                )}
                {check && check !== 'running' && <LayoutCheck result={check} />}
                {usage && <PipelineUsage repoId={repo.id} versions={versions} />}
                {error && <AlertError className="mt-2" error={error} onDismiss={() => setError(null)} />}
            </Card.Body>
        </Card>
    );
};

const DatasetList = () => {
    const [repos, setRepos] = useState(null);
    const [error, setError] = useState(null);
    useEffect(() => {
        listByPrefix(DATASET_PREFIX).then(setRepos).catch(setError);
    }, []);
    return (
        <>
            <p className="text-muted">
                Each dataset is its own repository. Release a version when its data is ready; pipelines offer every
                version in their dataset dropdown.
            </p>
            {error && <AlertError error={error} />}
            {!error && repos === null && <Loading />}
            {repos && repos.length === 0 && (
                <Card body className="text-center">
                    <p className="mb-2">There are no datasets in this project yet.</p>
                    <p className="text-muted mb-3">
                        Create one from a layout, add your data (in the browser or with the CLI), then release a version
                        for pipelines to use.
                    </p>
                    <div>
                        <Link className="btn btn-success" href={{ pathname: '/datasets/new' }}>
                            New dataset
                        </Link>
                    </div>
                </Card>
            )}
            {repos && repos.map((repo) => <DatasetCard key={repo.id} repo={repo} />)}
            <SharedWithUs />
        </>
    );
};

const DATASETS_TAB = { key: 'datasets', title: 'Datasets' };
const MELUXINA_TAB = { key: 'meluxina', title: 'MeluXina' };

const DatasetsPage = () => {
    const [params, setParams] = useSearchParams();
    // The MeluXina tab only where this lakeFS serves the view (a project that trains on MeluXina).
    const tabs = useUIConfig().meluxina ? [DATASETS_TAB, MELUXINA_TAB] : [DATASETS_TAB];
    const tab = tabs.some((t) => t.key === params.get('tab')) ? params.get('tab') : 'datasets';
    return (
        <Container fluid="xl" className="mt-3">
            <div className="d-flex align-items-center mb-3">
                <h3 className="mb-0">Datasets</h3>
                <Link className="btn btn-success ms-auto" href={{ pathname: '/datasets/new' }}>
                    New dataset
                </Link>
            </div>
            <Nav
                variant="tabs"
                className="mb-3"
                activeKey={tab}
                onSelect={(k) => setParams(k === 'datasets' ? {} : { tab: k })}
            >
                {tabs.map((t) => (
                    <Nav.Item key={t.key}>
                        <Nav.Link eventKey={t.key}>{t.title}</Nav.Link>
                    </Nav.Item>
                ))}
            </Nav>
            {tab === 'datasets' ? <DatasetList /> : <MeluxinaTab />}
        </Container>
    );
};

/** One dataset on its own page; ?created=1 (after New dataset) shows the next steps. */
export const DatasetPage = () => {
    const { repoId } = useParams();
    const [params] = useSearchParams();
    return (
        <Container fluid="xl" className="mt-3">
            <div className="mb-2">
                <Link href={{ pathname: '/datasets' }}>← All datasets</Link>
            </div>
            <DatasetCard key={repoId} repo={{ id: repoId }} detail created={params.get('created') === '1'} />
        </Container>
    );
};

export default DatasetsPage;
