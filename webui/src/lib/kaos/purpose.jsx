// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03): new file, Apache-2.0.
//
// A repository's purpose as people see it (P-1): a coloured badge from its kaos.* labels.
import React, { useEffect, useState } from 'react';
import Badge from 'react-bootstrap/Badge';

import { KIND, repoMetadata } from './api';

export const PURPOSE_COLOURS = { dataset: '#2e7d32', outputs: '#6f42c1', other: '#6c757d' };

/** The repository's kaos.* labels, or {} while loading or when unreadable. */
export const useRepoLabels = (repoId) => {
    const [labels, setLabels] = useState({});
    useEffect(() => {
        let live = true;
        repoMetadata(repoId)
            .then((md) => live && setLabels(md || {}))
            .catch(() => live && setLabels({}));
        return () => {
            live = false;
        };
    }, [repoId]);
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

/** Dataset (green, Default), Outputs (violet, with the app), Orphaned, or Other (grey). */
export const PurposeBadge = ({ labels }) => {
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
