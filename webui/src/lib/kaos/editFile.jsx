// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-05): new file, Apache-2.0.
//
// Edit a text file of a repository in the UI. A yolo dataset's data.yaml opens as a class-list
// form (kaos-lake parses and re-renders it, so nc always matches); other text files open in a text
// editor. Saving never writes main directly: it opens a pull request from a draft branch (OQ-2).
import React, { useState } from 'react';
import Button from 'react-bootstrap/Button';
import Form from 'react-bootstrap/Form';
import Modal from 'react-bootstrap/Modal';

import { objects } from '../api';
import { AlertError, Loading } from '../components/controls';
import { Link } from '../components/nav';
import { parseTemplate, proposeChange, renderTemplate } from './api';
import { useRepoLabels } from './purpose';
import { canonicalTemplate } from './templates';
import { pullsHref } from './pipeline';

export const MAX_EDIT_BYTES = 1 << 20;
const TEXT_EXTENSIONS = ['yaml', 'yml', 'md', 'txt', 'json', 'csv', 'toml', 'ini', 'py', 'cfg'];

/** Whether a file can be edited here: text, and at most 1 MiB. */
export const editable = (path, sizeBytes, contentType) => {
    const ext = path.split('.').pop().toLowerCase();
    const text = TEXT_EXTENSIONS.includes(ext) || (contentType || '').startsWith('text/');
    return text && sizeBytes >= 0 && sizeBytes <= MAX_EDIT_BYTES;
};

/** The layout whose class form edits path, when path is its form file (yolo: data.yaml at the root). */
export const formFor = (labels, path) =>
    canonicalTemplate(labels['kaos.template']) === 'yolo' && path === 'data.yaml' ? 'yolo' : '';

export const EditFileButton = ({ repoId, refId, path, sizeBytes, contentType }) => {
    const labels = useRepoLabels(repoId);
    const [open, setOpen] = useState(false);
    if (!editable(path, sizeBytes, contentType)) return null;
    return (
        <>
            <Button size="sm" variant="outline-primary" className="ms-2" onClick={() => setOpen(true)}>
                Edit
            </Button>
            {open && (
                <EditModal
                    repoId={repoId}
                    refId={refId}
                    path={path}
                    template={formFor(labels, path)}
                    description={labels['kaos.description'] || ''}
                    onClose={() => setOpen(false)}
                />
            )}
        </>
    );
};

const EditModal = ({ repoId, refId, path, template, description, onClose }) => {
    const [text, setText] = useState(null);
    const [classes, setClasses] = useState(null);
    const [title, setTitle] = useState(`Edit ${path}`);
    const [error, setError] = useState(null);
    const [saving, setSaving] = useState(false);
    const [pull, setPull] = useState(null);

    React.useEffect(() => {
        objects
            .get(repoId, refId, path)
            .then(async (content) => {
                setText(content);
                if (template) setClasses((await parseTemplate(template, content)).values.classes);
            })
            .catch(setError);
    }, [repoId, refId, path, template]);

    const save = async () => {
        setSaving(true);
        setError(null);
        try {
            let content = text;
            if (template && classes) {
                const { files } = await renderTemplate(template, repoId, description, { classes });
                content = files.find((f) => f.path === path).content;
            }
            setPull(await proposeChange(repoId, { [path]: content }, title));
        } catch (e) {
            setError(e);
        } finally {
            setSaving(false);
        }
    };

    return (
        <Modal show onHide={onClose} size="lg">
            <Modal.Header closeButton>
                <Modal.Title>Edit {path}</Modal.Title>
            </Modal.Header>
            <Modal.Body>
                {text === null && !error && <Loading />}
                {pull ? (
                    <div>
                        <p>
                            Pull request opened: <strong>{pull.title}</strong>. main changes only when it is merged.
                        </p>
                        <Link
                            className="btn btn-success me-2"
                            href={{
                                pathname: '/repositories/:repoId/pulls/:pullId',
                                params: { repoId, pullId: pull.id },
                            }}
                        >
                            Review and merge the pull request
                        </Link>
                        <Link href={pullsHref(repoId)}>All open pull requests</Link>
                    </div>
                ) : (
                    text !== null && (
                        <>
                            {template && classes ? (
                                <Form.Group className="mb-3">
                                    <Form.Label>Class names</Form.Label>
                                    <Form.Control
                                        as="textarea"
                                        rows={8}
                                        value={classes.join('\n')}
                                        onChange={(e) => setClasses(e.target.value.split('\n'))}
                                    />
                                    <Form.Text muted>
                                        One per line, in label-id order: the first class is id 0. nc follows the list.
                                    </Form.Text>
                                </Form.Group>
                            ) : (
                                <Form.Control
                                    as="textarea"
                                    rows={16}
                                    className="font-monospace"
                                    value={text}
                                    onChange={(e) => setText(e.target.value)}
                                />
                            )}
                            <Form.Group className="mt-3">
                                <Form.Label>Pull request title</Form.Label>
                                <Form.Control value={title} onChange={(e) => setTitle(e.target.value)} />
                            </Form.Group>
                        </>
                    )
                )}
                {error && <AlertError className="mt-2" error={error} onDismiss={() => setError(null)} />}
            </Modal.Body>
            <Modal.Footer>
                <Button variant="secondary" onClick={onClose}>
                    {pull ? 'Close' : 'Cancel'}
                </Button>
                {!pull && (
                    <Button variant="success" disabled={saving || text === null || !title.trim()} onClick={save}>
                        {saving ? 'Opening pull request…' : 'Open pull request'}
                    </Button>
                )}
            </Modal.Footer>
        </Modal>
    );
};
