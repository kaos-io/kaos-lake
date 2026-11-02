// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04): new file, Apache-2.0.
//
// The header's link back to the KAOS console, shown when kaos.ui.console_url is set.
import React, { useEffect, useState } from 'react';
import Nav from 'react-bootstrap/Nav';

import { getUIConfig } from './api';

/** The KAOS UI settings from the server, {} while loading or when not reachable. */
export const useUIConfig = () => {
    const [config, setConfig] = useState({});
    useEffect(() => {
        let live = true;
        getUIConfig()
            .then((c) => live && setConfig(c || {}))
            .catch(() => live && setConfig({}));
        return () => {
            live = false;
        };
    }, []);
    return config;
};

/**
 * A page of the KAOS console: `<console>/dashboard/<area>`, filtered to `project` when given (the
 * console's pages live under /dashboard; its Datasets and Models filters take the project name).
 * '' without a console.
 */
export const consoleHref = (consoleUrl, area, project = '') => {
    if (!consoleUrl) return '';
    const base = `${consoleUrl.replace(/\/+$/, '')}/dashboard/${area}`;
    return project ? `${base}?project=${encodeURIComponent(project)}` : base;
};

/** The console's page `area` for this lakeFS's project, or '' without a console. */
export const useConsoleHref = (area) => {
    const config = useUIConfig();
    return consoleHref(config.console_url || '', area, config.project || '');
};

/** "KAOS console" in the top bar, back to this project's Datasets; nothing without a console. */
export const ConsoleLink = () => {
    const href = useConsoleHref('datasets');
    if (!href) return null;
    return (
        <Nav.Link href={href} title="Back to this project's datasets in the KAOS console">
            ← KAOS console
        </Nav.Link>
    );
};
