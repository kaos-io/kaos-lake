// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-11): new file, Apache-2.0.
import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

const config = { console_url: 'https://acme.console.kaos.eu/', project: 'greenland' };
vi.mock('./api', () => ({ getUIConfig: async () => config }));

import { ConsoleLink, consoleHref } from './consoleLink';

describe('consoleHref', () => {
    it("opens the console's page under /dashboard, filtered to the project", () => {
        expect(consoleHref('https://acme.console.kaos.eu/', 'datasets', 'greenland')).toBe(
            'https://acme.console.kaos.eu/dashboard/datasets?project=greenland',
        );
        expect(consoleHref('https://acme.console.kaos.eu', 'models')).toBe(
            'https://acme.console.kaos.eu/dashboard/models',
        );
        expect(consoleHref('', 'datasets', 'greenland')).toBe('');
    });
});

describe('ConsoleLink', () => {
    it("goes back to this project's datasets", async () => {
        render(<ConsoleLink />);
        const link = await screen.findByText('← KAOS console');
        expect(link.getAttribute('href')).toBe('https://acme.console.kaos.eu/dashboard/datasets?project=greenland');
    });
});
