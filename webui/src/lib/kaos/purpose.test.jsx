// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03): new file, Apache-2.0.
import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

const uiConfig = { legacy_repository: 'e2e-proj' };
vi.mock('./consoleLink', () => ({ useUIConfig: () => uiConfig }));

import { PurposeBadge, PURPOSE_COLOURS } from './purpose';

describe('PurposeBadge', () => {
    it('shows a default dataset in green', () => {
        render(<PurposeBadge labels={{ 'kaos.kind': 'dataset', 'kaos.default': 'true' }} />);
        expect(screen.getByText('Dataset').style.backgroundColor).toBe(PURPOSE_COLOURS.dataset);
        expect(screen.getByText('Default')).toBeTruthy();
    });
    it("shows outputs as the app's model in violet, and an orphan warning", () => {
        render(<PurposeBadge labels={{ 'kaos.kind': 'outputs', 'kaos.app': 'yolo', 'kaos.orphaned': 'true' }} />);
        const badge = screen.getByText('Model · yolo');
        expect(badge.style.backgroundColor).toBe(PURPOSE_COLOURS.outputs);
        expect(badge.title).toBe(
            'The model of app yolo: its checkpoints, trained weights and exports, written by its pipeline runs',
        );
        expect(screen.getByText('Orphaned')).toBeTruthy();
        expect(screen.queryByText(/Outputs/)).toBeNull();
    });
    it('shows anything else as other', () => {
        render(<PurposeBadge labels={{}} />);
        expect(screen.getByText('Other')).toBeTruthy();
        expect(PURPOSE_COLOURS.other).toBe('#6c757d');
    });
    it("names the project's legacy repository instead of other", () => {
        render(<PurposeBadge labels={{}} repoId="e2e-proj" />);
        expect(screen.getByText('Project repository')).toBeTruthy();
        expect(screen.queryByText('Other')).toBeNull();
    });
    it('keeps other for any other unlabelled repository', () => {
        render(<PurposeBadge labels={{}} repoId="my-own-repo" />);
        expect(screen.getByText('Other')).toBeTruthy();
    });
    it('a labelled dataset named like the legacy repository is still a dataset', () => {
        render(<PurposeBadge labels={{ 'kaos.kind': 'dataset' }} repoId="e2e-proj" />);
        expect(screen.getByText('Dataset')).toBeTruthy();
        expect(screen.queryByText('Project repository')).toBeNull();
    });
    it('shows who a dataset is shared with (read-only)', () => {
        render(<PurposeBadge labels={{ 'kaos.kind': 'dataset', 'kaos.shared_with': ' orbits ,vision,' }} />);
        expect(screen.getByText('Shared with orbits, vision')).toBeTruthy();
    });
});

import { editable, formFor, MAX_EDIT_BYTES } from './editFile';

describe('editFile', () => {
    it('edits text files up to 1 MiB only', () => {
        expect(editable('data.yaml', 120, undefined)).toBe(true);
        expect(editable('notes', 10, 'text/plain')).toBe(true);
        expect(editable('images/a.jpg', 10, 'image/jpeg')).toBe(false);
        expect(editable('big.csv', MAX_EDIT_BYTES + 1, undefined)).toBe(false);
    });
    it("opens the class form only for a yolo dataset's root data.yaml (a legacy label too)", () => {
        expect(formFor({ 'kaos.template': 'yolo' }, 'data.yaml')).toBe('yolo');
        expect(formFor({ 'kaos.template': 'ultralytics' }, 'data.yaml')).toBe('yolo');
        expect(formFor({ 'kaos.template': 'yolo' }, 'sub/data.yaml')).toBe('');
        expect(formFor({ 'kaos.template': 'empty' }, 'data.yaml')).toBe('');
    });
});

describe('PlaceBadges', () => {
    it('shows where the data lives and who it is shared with', () => {
        render(
            <PurposeBadge
                labels={{
                    'kaos.kind': 'dataset',
                    'kaos.location': 'europe-west1 · Belgium',
                    'kaos.shared_with': 'orbits, vision',
                }}
            />,
        );
        expect(screen.getByText('📍 europe-west1 · Belgium')).toBeTruthy();
        expect(screen.getByText('Shared with orbits, vision')).toBeTruthy();
    });
});
