// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04): new file, Apache-2.0.
//
// Template names. "yolo" was renamed "ultralytics" (the format is Ultralytics'); datasets labelled
// kaos.template=yolo before the rename keep working, and kaos-lake still accepts the old name.

export const TEMPLATE_ALIASES = { yolo: 'ultralytics' };

export const TEMPLATE_TITLES = { ultralytics: 'Ultralytics (YOLO object detection)', empty: 'Empty' };

/** The current name of a template: the old name "yolo" is "ultralytics". */
export const canonicalTemplate = (name) => TEMPLATE_ALIASES[name] || name || '';

/** How people see a template's name. */
export const templateTitle = (name) => TEMPLATE_TITLES[canonicalTemplate(name)] || name || '';
