// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03): new file, Apache-2.0.
//
// A repository's purpose as people see it (P-1): a coloured badge from its kaos.* labels.
import React, { useEffect, useState } from 'react';
import Badge from 'react-bootstrap/Badge';

import { KIND, repoMetadata } from './api';

export const PURPOSE_COLOURS = { dataset: '#2e7d32', outputs: '#6f42c1', other: '#6c757d' };

/** The repository's kaos.* labels, or {} while loading or when unreadable. */
export const useRepoLabels = (repoId, refresh = 0) => {
    const [labels, setLabels] = useState({});
    useEffect(() => {
        let live = true;
        repoMetadata(repoId)
            .then((md) => live && setLabels(md || {}))
            .catch(() => live && setLabels({}));
        return () => {
            live = false;
        };
    }, [repoId, refresh]);
    return labels;
};

const Pill = ({ colour, title, children }) => (
    <Badge
        pill
        bg=""
        className="ms-1"
        title={title}
        style={{ backgroundColor: colour, color: '#fff', fontWeight: 500 }}
    >
        {children}
    </Badge>
);

/** Where the repository's data lives (kaos.location, set by the platform), and who it is shared with. */
export const PlaceBadges = ({ labels }) => (
    <>
        {labels['kaos.location'] && (
            <Pill colour="#0b5394" title="Where this repository's data is stored">
                📍 {labels['kaos.location']}
            </Pill>
        )}
        {labels['kaos.shared_with'] && (
            <Pill colour="#00796b" title="Projects that may read this dataset">
                Shared with{' '}
                {labels['kaos.shared_with']
                    .split(',')
                    .map((p) => p.trim())
                    .filter(Boolean)
                    .join(', ')}
            </Pill>
        )}
    </>
);

/** Dataset (green, Default), Outputs (violet, with the app), Orphaned, or Other (grey); then place. */
export const PurposeBadge = ({ labels }) => (
    <>
        <KindBadge labels={labels} />
        <PlaceBadges labels={labels} />
    </>
);

const KindBadge = ({ labels }) => {
    const kind = labels['kaos.kind'];
    if (kind === KIND.dataset) {
        return (
            <>
                <Pill colour={PURPOSE_COLOURS.dataset} title="A dataset: pipelines can train on its versions">
                    Dataset
                </Pill>
                {labels['kaos.default'] === 'true' && (
                    <Pill colour={PURPOSE_COLOURS.dataset} title="The project's default dataset">
                        Default
                    </Pill>
                )}
            </>
        );
    }
    if (kind === KIND.outputs) {
        return (
            <>
                <Pill colour={PURPOSE_COLOURS.outputs} title="Checkpoints and results of one ML app">
                    Outputs{labels['kaos.app'] ? ` · ${labels['kaos.app']}` : ''}
                </Pill>
                {labels['kaos.orphaned'] === 'true' && (
                    <Pill colour="#b26a00" title="No app declares this repository any more; it is kept">
                        Orphaned
                    </Pill>
                )}
            </>
        );
    }
    return (
        <Pill colour={PURPOSE_COLOURS.other} title="Not a dataset or app outputs">
            Other
        </Pill>
    );
};

/** PurposeBadge for one repository, reading its labels. */
export const RepoPurposeBadge = ({ repoId }) => <PurposeBadge labels={useRepoLabels(repoId)} />;
