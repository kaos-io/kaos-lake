// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-06): new file, Apache-2.0.
//
// The Datasets page: the project's dataset-* repositories with their purpose, description,
// template and latest version, a Release version action, and New dataset.
import React, { useCallback, useEffect, useState } from 'react';
import Button from 'react-bootstrap/Button';
import Card from 'react-bootstrap/Card';
import Container from 'react-bootstrap/Container';

import { AlertError, Loading } from '../../lib/components/controls';
import { Link } from '../../lib/components/nav';
import Form from 'react-bootstrap/Form';
import InputGroup from 'react-bootstrap/InputGroup';

import { DATASET_PREFIX, listByPrefix, listVersions, releaseVersion, setRepoMetadata } from '../../lib/kaos/api';
import { PurposeBadge, useRepoLabels } from '../../lib/kaos/purpose';

/** Share a dataset with other projects of the organisation (D-09); only the project's admins may. */
const ShareForm = ({ repoId, labels, onDone }) => {
    const [value, setValue] = useState(labels['kaos.shared_with'] || '');
    const [error, setError] = useState(null);
    const [saving, setSaving] = useState(false);
    const save = async (e) => {
        e.preventDefault();
        setSaving(true);
        setError(null);
        try {
            const projects = value
                .split(',')
                .map((p) => p.trim())
                .filter(Boolean)
                .join(',');
            await setRepoMetadata(repoId, { 'kaos.shared_with': projects });
            onDone();
        } catch (err) {
            setError(err);
        } finally {
            setSaving(false);
        }
    };
    return (
        <Form onSubmit={save} className="mt-2">
            <InputGroup size="sm">
                <InputGroup.Text>Share with projects</InputGroup.Text>
                <Form.Control value={value} placeholder="orbits, vision" onChange={(e) => setValue(e.target.value)} />
                <Button type="submit" size="sm" variant="success" disabled={saving}>
                    {saving ? 'Saving…' : 'Save'}
                </Button>
            </InputGroup>
            <Form.Text muted>
                Read-only for those projects, in this organisation. The data stays where it is
                {labels['kaos.location'] ? ` (${labels['kaos.location']})` : ''}; they read it from there. Empty stops
                sharing.
            </Form.Text>
            {error && <AlertError className="mt-2" error={error} onDismiss={() => setError(null)} />}
        </Form>
    );
};

const DatasetCard = ({ repo }) => {
    const [refresh, setRefresh] = useState(0);
    const labels = useRepoLabels(repo.id, refresh);
    const [sharing, setSharing] = useState(false);
    const [versions, setVersions] = useState(null);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState(null);
    const [released, setReleased] = useState('');
    const load = useCallback(() => {
        listVersions(repo.id)
            .then(setVersions)
            .catch(() => setVersions([]));
    }, [repo.id]);
    useEffect(load, [load]);
    const release = async () => {
        setBusy(true);
        setError(null);
        try {
            const tag = await releaseVersion(repo.id);
            setReleased(tag.id);
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
                <div className="d-flex justify-content-between align-items-start">
                    <div>
                        <h5 className="mb-1">
                            <Link href={{ pathname: '/repositories/:repoId/objects', params: { repoId: repo.id } }}>
                                {repo.id}
                            </Link>
                            <PurposeBadge labels={labels} />
                        </h5>
                        {labels['kaos.description'] && <div className="text-muted">{labels['kaos.description']}</div>}
                        <div className="small text-muted mt-1">
                            {labels['kaos.template'] ? `Template: ${labels['kaos.template']} · ` : ''}
                            {versions === null
                                ? 'Reading versions…'
                                : latest
                                  ? `Latest version: ${latest.id} (${latest.commit_id.slice(0, 12)})`
                                  : 'No version released yet'}
                        </div>
                    </div>
                    <div className="d-flex gap-2">
                        <Button size="sm" variant="outline-secondary" onClick={() => setSharing(!sharing)}>
                            Share
                        </Button>
                        <Button
                            size="sm"
                            variant="outline-success"
                            disabled={busy}
                            onClick={release}
                            title="Tag main's head as the next version; pipelines can then pick it"
                        >
                            {busy ? 'Releasing…' : 'Release version'}
                        </Button>
                    </div>
                </div>
                {sharing && (
                    <ShareForm
                        repoId={repo.id}
                        labels={labels}
                        onDone={() => {
                            setSharing(false);
                            setRefresh(refresh + 1);
                        }}
                    />
                )}
                {released && <div className="small text-success mt-2">Released {released}.</div>}
                {error && <AlertError className="mt-2" error={error} onDismiss={() => setError(null)} />}
            </Card.Body>
        </Card>
    );
};

const DatasetsPage = () => {
    const [repos, setRepos] = useState(null);
    const [error, setError] = useState(null);
    useEffect(() => {
        listByPrefix(DATASET_PREFIX).then(setRepos).catch(setError);
    }, []);
    return (
        <Container fluid="xl" className="mt-3">
            <div className="d-flex align-items-center mb-3">
                <h3 className="mb-0">Datasets</h3>
                <Link className="btn btn-success ms-auto" href={{ pathname: '/datasets/new' }}>
                    New dataset
                </Link>
            </div>
            <p className="text-muted">
                Each dataset is its own repository. Release a version when its data is ready; pipelines offer every
                version in their dataset dropdown.
            </p>
            {error && <AlertError error={error} />}
            {!error && repos === null && <Loading />}
            {repos && repos.length === 0 && <p>No datasets yet. Create one from a template.</p>}
            {repos && repos.map((repo) => <DatasetCard key={repo.id} repo={repo} />)}
        </Container>
    );
};

export default DatasetsPage;
