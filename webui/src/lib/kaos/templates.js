// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04; dataset layouts): new file, Apache-2.0.
//
// Layout names (kaos.template). Datasets may carry an id from before their layout got its current
// name: it reads, edits and shows as that layout, and is never written.

export const LAYOUT_ALIASES = { ultralytics: 'yolo', empty: 'raw' };

export const LAYOUT_TITLES = { yolo: 'YOLO (txt annotations)', raw: 'Raw (any files)' };

/** The current name of a layout id. */
export const canonicalTemplate = (name) => LAYOUT_ALIASES[name] || name || '';

/** How people see a layout's name (a project layout shows as its name). */
export const templateTitle = (name) => LAYOUT_TITLES[canonicalTemplate(name)] || name || '';
