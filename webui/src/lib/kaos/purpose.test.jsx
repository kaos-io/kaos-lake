// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03): new file, Apache-2.0.
import React from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';

import { PurposeBadge, PURPOSE_COLOURS } from './purpose';

describe('PurposeBadge', () => {
    it('shows a default dataset in green', () => {
        render(<PurposeBadge labels={{ 'kaos.kind': 'dataset', 'kaos.default': 'true' }} />);
        expect(screen.getByText('Dataset').style.backgroundColor).toBe(PURPOSE_COLOURS.dataset);
        expect(screen.getByText('Default')).toBeTruthy();
    });
    it('shows outputs in violet with their app, and an orphan warning', () => {
        render(<PurposeBadge labels={{ 'kaos.kind': 'outputs', 'kaos.app': 'yolo', 'kaos.orphaned': 'true' }} />);
        expect(screen.getByText('Outputs · yolo').style.backgroundColor).toBe(PURPOSE_COLOURS.outputs);
        expect(screen.getByText('Orphaned')).toBeTruthy();
    });
    it('shows anything else as other', () => {
        render(<PurposeBadge labels={{}} />);
        expect(screen.getByText('Other')).toBeTruthy();
        expect(PURPOSE_COLOURS.other).toBe('#6c757d');
    });
});
