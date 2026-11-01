// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-07): new file, Apache-2.0.
//
// "How to use in a pipeline": the exact `dataset = <repo>@<version>` string for each version of a
// dataset, with a copy button, and links from the version to its data, commit and tag.
import React, { useState } from 'react';
import Button from 'react-bootstrap/Button';

import { ClipboardButton } from '../components/controls';
import { Link } from '../components/nav';
import { browseHref, commitHref, pipelineRef, tagsHref } from './pipeline';

const SHOWN = 10;

const Row = ({ repoId, version, note, commitId }) => (
    <div className="d-flex align-items-center flex-wrap gap-2 py-1 border-bottom">
        <code className="user-select-all">{pipelineRef(repoId, version)}</code>
        <ClipboardButton
            variant="link"
            size="sm"
            text={pipelineRef(repoId, version)}
            tooltip={`Copy dataset = ${repoId}@${version}`}
        />
        <span className="small text-muted ms-auto">
            {note}
            <Link className="ms-2" href={browseHref(repoId, version === 'latest' ? 'main' : version)}>
                Browse data
            </Link>
            {commitId && (
                <>
                    <Link className="ms-2" href={commitHref(repoId, commitId)}>
                        Commit {commitId.slice(0, 12)}
                    </Link>
                    <Link className="ms-2" href={tagsHref(repoId)}>
                        Tag page
                    </Link>
                </>
            )}
        </span>
    </div>
);

/** versions: the dataset's tags vN, newest first (null while loading). */
export const PipelineUsage = ({ repoId, versions }) => {
    const [all, setAll] = useState(false);
    const list = versions || [];
    const shown = all ? list : list.slice(0, SHOWN);
    return (
        <div className="mt-3 p-3 border rounded bg-body-tertiary">
            <h6 className="mb-1">How to use in a pipeline</h6>
            <div className="small text-muted mb-2">
                Set the pipeline&apos;s dataset parameter to one of these. A version never changes, so a rerun trains on
                the same data; <code>latest</code> follows main and is for trying things out.
            </div>
            <Row repoId={repoId} version="latest" note="the head of main, not pinned" />
            {versions === null && <div className="small text-muted py-1">Reading versions…</div>}
            {versions !== null && list.length === 0 && (
                <div className="small text-muted py-1">
                    No version yet. Upload data, then use <strong>Release version</strong> to get <code>@v1</code>.
                </div>
            )}
            {shown.map((v) => (
                <Row key={v.id} repoId={repoId} version={v.id} commitId={v.commit_id} />
            ))}
            {list.length > SHOWN && (
                <Button size="sm" variant="link" onClick={() => setAll(!all)}>
                    {all ? 'Show fewer' : `Show all ${list.length} versions`}
                </Button>
            )}
        </div>
    );
};
