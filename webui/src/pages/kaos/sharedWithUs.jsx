// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.
//
// "Shared with this project" on the Datasets page: the datasets other projects of the
// organisation share with this one (GET /kaos/v1/shared-with-us), read-only. Each names its owner,
// says how to use it in a pipeline and links to it in the owner's lakeFS; the owner releases its
// versions and decides who it is shared with, so there is nothing to release, edit or share here.
import React, { useEffect, useState } from 'react';
import Card from 'react-bootstrap/Card';

import { ClipboardButton } from '../../lib/components/controls';
import { getSharedWithUs } from '../../lib/kaos/api';
import { pipelineRef } from '../../lib/kaos/pipeline';
import { PlaceBadges, SharedByBadge } from '../../lib/kaos/purpose';
import { templateTitle } from '../../lib/kaos/templates';

export const SharedDatasetRow = ({ dataset }) => {
    const latest = dataset.versions && dataset.versions.length ? dataset.versions[0] : null;
    const ref = pipelineRef(dataset.name, latest ? latest.tag : 'latest');
    return (
        <div className="py-3 border-bottom" data-testid="shared-dataset">
            <div className="d-flex justify-content-between align-items-start flex-wrap gap-2">
                <div>
                    <h5 className="mb-1">
                        {dataset.name}
                        <SharedByBadge owner={dataset.owner} />
                        <PlaceBadges labels={{ 'kaos.location': dataset.location }} />
                    </h5>
                    {dataset.description && <div className="text-muted">{dataset.description}</div>}
                    <div className="small text-muted mt-1">
                        {dataset.template ? `Template: ${templateTitle(dataset.template)} · ` : ''}
                        {latest
                            ? `Latest version: ${latest.tag} (${(latest.commit || '').slice(0, 12)})`
                            : 'No version released yet'}
                    </div>
                </div>
                {dataset.url && (
                    <a
                        className="btn btn-sm btn-outline-secondary"
                        href={dataset.url}
                        target="_blank"
                        rel="noreferrer"
                        title={`Browse ${dataset.repo} in project ${dataset.owner}'s lakeFS`}
                    >
                        Open in {dataset.owner}&apos;s lakeFS ↗
                    </a>
                )}
            </div>
            <div className="small mt-2">
                How to use in a pipeline: <code className="user-select-all">{ref}</code>
                <ClipboardButton variant="link" size="sm" text={ref} tooltip={`Copy ${ref}`} />
                {!latest && <span className="text-muted">(follows main until {dataset.owner} releases a version)</span>}
            </div>
        </div>
    );
};

/** The section, below the project's own datasets; nothing at all when nothing is shared. */
export const SharedWithUs = () => {
    const [shared, setShared] = useState(null);
    const [error, setError] = useState(null);
    useEffect(() => {
        let live = true;
        getSharedWithUs()
            .then((s) => live && setShared(s))
            .catch((e) => live && setError(e));
        return () => {
            live = false;
        };
    }, []);
    if (error) {
        return (
            <div className="small text-muted mt-3">
                Could not read the datasets other projects share with this one: {error.message}
            </div>
        );
    }
    if (shared === null) {
        return <div className="small text-muted mt-3">Reading the datasets other projects share with this one…</div>;
    }
    if (shared.datasets.length === 0 && shared.notes.length === 0) return null;
    return (
        <Card className="mt-4 mb-3">
            <Card.Header as="h5">Shared with this project</Card.Header>
            <Card.Body className="pt-2">
                <div className="small text-muted mb-1">
                    Other projects of the organisation share these datasets with this one. Pipelines can train on them;
                    their owners release the versions and decide who they are shared with.
                </div>
                {shared.notes.map((n) => (
                    <div key={n.owner} className="small text-muted fst-italic">
                        {n.note}
                    </div>
                ))}
                {shared.datasets.map((d) => (
                    <SharedDatasetRow key={d.name} dataset={d} />
                ))}
            </Card.Body>
        </Card>
    );
};

export default SharedWithUs;
