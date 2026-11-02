// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04): new file, Apache-2.0.
//
// New dataset: a name (dataset-…), a description, a layout (built-in or the project's own) and its
// parameters. The dataset is created and its first commit made with the signed-in person's own
// identity.
import React, { useEffect, useState } from 'react';
import Badge from 'react-bootstrap/Badge';
import Button from 'react-bootstrap/Button';
import Card from 'react-bootstrap/Card';
import Container from 'react-bootstrap/Container';
import Form from 'react-bootstrap/Form';
import InputGroup from 'react-bootstrap/InputGroup';

import { AlertError, Loading } from '../../lib/components/controls';
import { Link } from '../../lib/components/nav';
import { useRouter } from '../../lib/hooks/router';
import { DATASET_PREFIX, createDataset, listTemplates } from '../../lib/kaos/api';
import { datasetHref } from '../../lib/kaos/pipeline';

const NAME_REST = /^[a-z0-9][a-z0-9-]{2,47}$/;

export const FieldInput = ({ field, value, onChange }) =>
    field.type === 'list' ? (
        <Form.Control
            as="textarea"
            rows={6}
            value={(value || []).join('\n')}
            onChange={(e) => onChange(e.target.value.split('\n'))}
        />
    ) : (
        <Form.Control value={value || ''} onChange={(e) => onChange(e.target.value)} />
    );

const NewDatasetPage = () => {
    const router = useRouter();
    const [catalog, setCatalog] = useState(null);
    const [error, setError] = useState(null);
    const [rest, setRest] = useState('');
    const [description, setDescription] = useState('');
    const [template, setTemplate] = useState('yolo');
    const [values, setValues] = useState({});
    const [step, setStep] = useState('');

    useEffect(() => {
        listTemplates().then(setCatalog).catch(setError);
    }, []);
    if (!catalog) return error ? <AlertError error={error} /> : <Loading />;

    const chosen = catalog.templates.find((t) => t.name === template) || catalog.templates[0];
    const name = DATASET_PREFIX + rest;
    const nameOk = NAME_REST.test(rest);
    const create = async (e) => {
        e.preventDefault();
        setError(null);
        try {
            await createDataset(
                { name, description, template: chosen.name, values, storagePrefix: catalog.storage_namespace_prefix },
                setStep,
            );
            // Land on the dataset, with the next steps (add data, release a version).
            router.push(datasetHref(name, { created: '1' }));
        } catch (err) {
            setError(err);
            setStep('');
        }
    };
    return (
        <Container fluid="md" className="mt-3">
            <div className="mb-2">
                <Link href={{ pathname: '/datasets' }}>← All datasets</Link>
            </div>
            <h3>New dataset</h3>
            <p className="text-muted">
                Pick a layout and a name. The dataset is created as you, with the layout&apos;s folders and files; then
                you add your data and release a version.
            </p>
            {!catalog.storage_namespace_prefix && (
                <AlertError error="This lakeFS has no storage for new datasets (kaos.repositories.storage_namespace_prefix)." />
            )}
            <Form onSubmit={create}>
                <Form.Group className="mb-3">
                    <Form.Label>Name</Form.Label>
                    <InputGroup>
                        <InputGroup.Text>{DATASET_PREFIX}</InputGroup.Text>
                        <Form.Control
                            value={rest}
                            isInvalid={rest !== '' && !nameOk}
                            placeholder="cars"
                            onChange={(e) => setRest(e.target.value.trim().toLowerCase())}
                        />
                        <Form.Control.Feedback type="invalid">
                            3–48 lowercase letters, digits or hyphens, starting with a letter or digit.
                        </Form.Control.Feedback>
                    </InputGroup>
                </Form.Group>
                <Form.Group className="mb-3">
                    <Form.Label>Description</Form.Label>
                    <Form.Control value={description} onChange={(e) => setDescription(e.target.value)} />
                </Form.Group>
                <Form.Label>Layout</Form.Label>
                <div className="d-flex gap-3 mb-3 flex-wrap">
                    {catalog.templates.map((t) => (
                        <Card
                            key={t.name}
                            role="button"
                            onClick={() => setTemplate(t.name)}
                            border={t.name === chosen.name ? 'success' : undefined}
                            style={{ width: '18rem', cursor: 'pointer' }}
                        >
                            <Card.Body>
                                <Card.Title>
                                    {t.title}{' '}
                                    <Badge
                                        bg={t.source === 'project' ? 'info' : 'secondary'}
                                        className="fs-6 fw-normal"
                                    >
                                        {t.source === 'project' ? 'Project' : 'Built-in'}
                                    </Badge>
                                </Card.Title>
                                <Card.Text className="small text-muted">{t.description}</Card.Text>
                                <Card.Text className="small text-muted">
                                    <code>{t.name}</code>
                                    {t.version ? ` · version ${t.version}` : ''}
                                </Card.Text>
                            </Card.Body>
                        </Card>
                    ))}
                </div>
                <p className="small text-muted">
                    Your project can add its own layouts: a <code>layout.yaml</code> in the{' '}
                    <code>{catalog.layouts_repository || 'layouts'}</code> repository (<code>kaos-cli layout init</code>
                    , then <code>kaos-cli layout publish</code>). kaos-lake checks a layout&apos;s declarative rules
                    only; a layout&apos;s Python hook runs only in kaos-cli.
                </p>
                {(catalog.problems || []).map((p) => (
                    <AlertError key={p.path} error={`Layout not offered: ${p.error}`} />
                ))}
                {(chosen.parameters || []).map((f) => (
                    <Form.Group className="mb-3" key={f.key}>
                        <Form.Label>
                            {f.label}
                            {f.required ? ' *' : ''}
                        </Form.Label>
                        <FieldInput
                            field={f}
                            value={values[f.key]}
                            onChange={(v) => setValues({ ...values, [f.key]: v })}
                        />
                        {f.help && <Form.Text muted>{f.help}</Form.Text>}
                    </Form.Group>
                ))}
                {error && <AlertError error={error} onDismiss={() => setError(null)} />}
                <Button
                    type="submit"
                    variant="success"
                    disabled={!nameOk || !!step || !catalog.storage_namespace_prefix}
                >
                    {step ? `${step}…` : `Create ${name}`}
                </Button>
            </Form>
        </Container>
    );
};

export default NewDatasetPage;
