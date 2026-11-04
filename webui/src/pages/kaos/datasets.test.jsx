// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-10): new file, Apache-2.0.
import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const uiConfig = { meluxina: false };
vi.mock('../../lib/kaos/consoleLink', () => ({ useUIConfig: () => uiConfig, useConsoleHref: () => '' }));
vi.mock('../../lib/kaos/api', async (orig) => ({
    ...(await orig()),
    listByPrefix: async () => [],
    getSharedWithUs: async () => ({ project: 'vision', datasets: [], notes: [] }),
}));
vi.mock('./meluxina', () => ({ default: () => <div>meluxina view</div> }));

import DatasetsPage from './datasets';

const at = (url) =>
    render(
        <MemoryRouter initialEntries={[url]}>
            <DatasetsPage />
        </MemoryRouter>,
    );

describe('Datasets page tabs', () => {
    it('has no HPC tab where lakeFS does not serve the view', () => {
        uiConfig.meluxina = false;
        at('/datasets?tab=meluxina');
        expect(screen.queryByText('HPC')).toBeNull();
        expect(screen.queryByText('meluxina view')).toBeNull();
    });
    it('shows the HPC tab on a project that trains on HPC', () => {
        uiConfig.meluxina = true;
        at('/datasets?tab=meluxina');
        expect(screen.getByText('HPC')).toBeTruthy();
        expect(screen.getByText('meluxina view')).toBeTruthy();
    });
});

describe('LayoutCheck', () => {
    it('lists what the rules found and says the hook runs only in kaosctl', async () => {
        const { LayoutCheck } = await import('./datasets');
        render(
            <LayoutCheck
                result={{
                    layout: 'yolo',
                    hook: 'kaos_lib.layouts.yolo',
                    problems: ['images/train/a.jpg has no labels/train/a.txt'],
                    checked: { files: 3, read: 1, skipped: 0, partial: false },
                }}
            />,
        );
        expect(screen.getByText(/1 problem on main for layout yolo/)).toBeTruthy();
        expect(screen.getByText('images/train/a.jpg has no labels/train/a.txt')).toBeTruthy();
        expect(screen.getByText('kaos_lib.layouts.yolo')).toBeTruthy();
        expect(screen.getByText(/declarative rules only/)).toBeTruthy();
    });
    it('says when main follows the rules and when the check was partial', async () => {
        const { LayoutCheck } = await import('./datasets');
        render(
            <LayoutCheck
                result={{ layout: 'samples', problems: [], checked: { files: 9, read: 1, skipped: 4, partial: true } }}
            />,
        );
        expect(screen.getByText(/Main follows layout samples/)).toBeTruthy();
        expect(screen.getByText(/4 files too large or too many/)).toBeTruthy();
    });
});
