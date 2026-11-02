// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const answer = { current: null };
vi.mock('../../lib/kaos/api', async (orig) => ({
    ...(await orig()),
    getSharedWithUs: vi.fn(() => answer.current),
}));
vi.mock('../../lib/kaos/consoleLink', () => ({ useUIConfig: () => ({}), useConsoleHref: () => '' }));

import { PURPOSE_COLOURS } from '../../lib/kaos/purpose';
import SharedWithUs from './sharedWithUs';

const roads = {
    name: 'cars/dataset-roads',
    owner: 'cars',
    repo: 'dataset-roads',
    location: 'europe-west1 · Belgium',
    description: 'Roads at night',
    template: 'ultralytics',
    latest: 'h1',
    versions: [
        { tag: 'v2', commit: 'c2c2c2c2c2c2c2c2' },
        { tag: 'v1', commit: 'c1' },
    ],
    url: 'https://lakefs-cars.acme.kaos.eu/repositories/dataset-roads/objects',
};
const stars = {
    name: 'orbits/dataset-stars',
    owner: 'orbits',
    repo: 'dataset-stars',
    versions: [],
    url: 'https://lakefs-orbits.acme.kaos.eu/repositories/dataset-stars/objects',
};

const show = (body) => {
    answer.current = body instanceof Error ? Promise.reject(body) : Promise.resolve(body);
    return render(
        <MemoryRouter>
            <SharedWithUs />
        </MemoryRouter>,
    );
};

beforeEach(() => {
    answer.current = null;
});

describe('Shared with this project', () => {
    it('lists each shared dataset with its owner, place, template, version, pipeline ref and link', async () => {
        show({ project: 'vision', datasets: [roads, stars], notes: [] });
        expect(await screen.findByText('Shared with this project')).toBeTruthy();
        expect(screen.getAllByTestId('shared-dataset')).toHaveLength(2);

        const badge = screen.getByText('Shared by cars');
        expect(PURPOSE_COLOURS.shared).toBe('#00796b'); // teal, as the owner's Shared with badge
        expect(badge.style.backgroundColor).toBe(PURPOSE_COLOURS.shared);
        expect(screen.getByText('📍 europe-west1 · Belgium')).toBeTruthy();
        expect(screen.getByText('Roads at night')).toBeTruthy();
        expect(screen.getByText(/Template: Ultralytics \(YOLO object detection\)/)).toBeTruthy();
        expect(screen.getByText(/Latest version: v2 \(c2c2c2c2c2c2\)/)).toBeTruthy();
        expect(screen.getByText('dataset = cars/dataset-roads@v2')).toBeTruthy();

        const open = screen.getByText(/Open in cars's lakeFS/).closest('a');
        expect(open.getAttribute('href')).toBe(roads.url);
        expect(open.getAttribute('target')).toBe('_blank');
        expect(open.getAttribute('rel')).toBe('noreferrer');

        // No version yet: latest, and no location or template badge it does not have.
        expect(screen.getByText('Shared by orbits')).toBeTruthy();
        expect(screen.getByText('dataset = orbits/dataset-stars@latest')).toBeTruthy();
        expect(screen.getByText('No version released yet')).toBeTruthy();
        expect(screen.getAllByText(/^📍/)).toHaveLength(1);
    });

    it('offers no release, edit or share controls', async () => {
        show({ project: 'vision', datasets: [roads], notes: [] });
        await screen.findByText('Shared with this project');
        for (const control of [/Release/i, /Edit/i, /^Share/i, /New dataset/i, /pull request/i]) {
            expect(screen.queryByRole('button', { name: control })).toBeNull();
            expect(screen.queryByRole('link', { name: control })).toBeNull();
        }
        // The only link leaves for the owner's lakeFS; the only button copies the pipeline ref.
        expect(screen.getAllByRole('link').map((a) => a.getAttribute('href'))).toEqual([roads.url]);
        expect(screen.getAllByRole('button')).toHaveLength(1);
    });

    it('shows notes for owners that could not be read, also without datasets', async () => {
        const note = "Project cars's lakeFS did not answer; showing its last known list.";
        const missing = "Project old's lakeFS is too old to share datasets; what it shares is not shown.";
        show({
            project: 'vision',
            datasets: [],
            notes: [
                { owner: 'cars', note },
                { owner: 'old', note: missing },
            ],
        });
        expect(await screen.findByText(note)).toBeTruthy();
        expect(screen.getByText(missing).className).toContain('text-muted');
        expect(screen.getByText('Shared with this project')).toBeTruthy();
        expect(screen.queryAllByTestId('shared-dataset')).toHaveLength(0);
    });

    it('is hidden when nothing is shared and there is no note', async () => {
        const { container } = show({ project: 'vision', datasets: [], notes: [] });
        await waitFor(() => expect(container.textContent).toBe(''));
        expect(screen.queryByText('Shared with this project')).toBeNull();
    });

    it('says it is reading while the answer is on its way', () => {
        answer.current = new Promise(() => {});
        render(<SharedWithUs />);
        expect(screen.getByText(/Reading the datasets other projects share/)).toBeTruthy();
        expect(screen.queryByText('Shared with this project')).toBeNull();
    });

    it('says so in a muted line when the read fails', async () => {
        show(new Error('503 Service Unavailable'));
        const line = await screen.findByText(/Could not read the datasets other projects share/);
        expect(line.textContent).toContain('503 Service Unavailable');
        expect(line.className).toContain('text-muted');
    });
});
