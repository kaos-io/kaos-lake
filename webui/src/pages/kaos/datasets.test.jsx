// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-10): new file, Apache-2.0.
import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const uiConfig = { meluxina: false };
vi.mock('../../lib/kaos/consoleLink', () => ({ useUIConfig: () => uiConfig, useConsoleUrl: () => '' }));
vi.mock('../../lib/kaos/api', async (orig) => ({ ...(await orig()), listByPrefix: async () => [] }));
vi.mock('./meluxina', () => ({ default: () => <div>meluxina view</div> }));

import DatasetsPage from './datasets';

const at = (url) =>
    render(
        <MemoryRouter initialEntries={[url]}>
            <DatasetsPage />
        </MemoryRouter>,
    );

describe('Datasets page tabs', () => {
    it('has no MeluXina tab where lakeFS does not serve the view', () => {
        uiConfig.meluxina = false;
        at('/datasets?tab=meluxina');
        expect(screen.queryByText('MeluXina')).toBeNull();
        expect(screen.queryByText('meluxina view')).toBeNull();
    });
    it('shows the MeluXina tab on a project that trains on MeluXina', () => {
        uiConfig.meluxina = true;
        at('/datasets?tab=meluxina');
        expect(screen.getByText('MeluXina')).toBeTruthy();
        expect(screen.getByText('meluxina view')).toBeTruthy();
    });
});
