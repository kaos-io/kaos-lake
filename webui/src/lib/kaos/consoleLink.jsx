// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04): new file, Apache-2.0.
//
// The header's link back to the KAOS console, shown when kaos.ui.console_url is set.
import React, { useEffect, useState } from 'react';
import Nav from 'react-bootstrap/Nav';

import { getUIConfig } from './api';

/** The console URL from the server, or '' (none, or not reachable). */
export const useConsoleUrl = () => {
    const [url, setUrl] = useState('');
    useEffect(() => {
        let live = true;
        getUIConfig()
            .then((c) => live && setUrl(c.console_url || ''))
            .catch(() => live && setUrl(''));
        return () => {
            live = false;
        };
    }, []);
    return url;
};

/** "KAOS console" in the top bar; nothing when no console is configured. */
export const ConsoleLink = () => {
    const url = useConsoleUrl();
    if (!url) return null;
    return (
        <Nav.Link href={url} title="Back to the KAOS console">
            ← KAOS console
        </Nav.Link>
    );
};
