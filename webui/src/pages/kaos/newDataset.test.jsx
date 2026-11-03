// KAOS (kaos-lake, dataset layouts): new file, Apache-2.0.
import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const catalog = {
    storage_namespace_prefix: 'gs://b/lake',
    layouts_repository: 'layouts',
    templates: [
        {
            name: 'raw',
            title: 'Raw (any files)',
            description: 'Only a README',
            source: 'builtin',
            version: '1',
            parameters: [],
        },
        {
            name: 'yolo',
            title: 'YOLO (txt annotations)',
            description: 'Images and labels',
            source: 'builtin',
            version: '1',
            parameters: [{ key: 'classes', label: 'Class names', type: 'list', required: true }],
        },
        { name: 'samples', title: 'Samples', description: 'Ours', source: 'project', version: '3', parameters: [] },
    ],
    problems: [
        {
            path: 'layouts/bad/layout.yaml',
            error: 'layouts/bad/layout.yaml is not a valid layout: (root): title is required',
        },
    ],
};
vi.mock('../../lib/kaos/api', async (orig) => ({ ...(await orig()), listTemplates: async () => catalog }));

import NewDatasetPage from './newDataset';

describe('New dataset', () => {
    it('offers no layout (chosen), the built-in and the project layouts, and says which were left out', async () => {
        render(
            <MemoryRouter initialEntries={['/datasets/new']}>
                <NewDatasetPage />
            </MemoryRouter>,
        );
        expect(await screen.findByText('Samples')).toBeTruthy();
        expect(screen.getByText('No layout')).toBeTruthy();
        expect(screen.getAllByText('Built-in')).toHaveLength(2);
        expect(screen.getByText('Project')).toBeTruthy();
        expect(screen.queryByText(/Class names/)).toBeNull(); // no layout chosen: no parameters
        fireEvent.click(screen.getByText('YOLO (txt annotations)'));
        expect(screen.getByText(/Class names/)).toBeTruthy(); // yolo picked on purpose
        expect(screen.getByText(/title is required/)).toBeTruthy();
        expect(screen.getByText('kaosctl layout publish')).toBeTruthy();
    });
});
