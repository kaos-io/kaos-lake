// KAOS (kaos-lake, PRD novelcore/kaos#1283): new file, Apache-2.0.
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

const lakefs = vi.hoisted(() => ({ tags: [], log: [], commits: {}, fetched: [] }));
vi.mock('../api', () => ({
    API_ENDPOINT: '/api/v1',
    defaultAPIHeaders: {},
    extractError: async () => 'refused',
    tags: { list: vi.fn(async () => ({ results: lakefs.tags, pagination: {} })) },
    commits: {
        get: vi.fn(async (repo, id) => {
            lakefs.fetched.push(id);
            if (!lakefs.commits[id]) throw new Error('not found');
            return lakefs.commits[id];
        }),
    },
}));
const uiConfig = vi.hoisted(() => ({ current: {} }));
vi.mock('./consoleLink', () => ({ useUIConfig: () => uiConfig.current }));

import { tags } from '../api';
import {
    DASH,
    consoleModelsUrl,
    datasetOf,
    loadModel,
    mlflowModelsUrl,
    mlflowRunUrl,
    modelVersions,
    parseModelTag,
    runRow,
} from './model';
import { DatasetRef, ModelHeader, ModelPanel } from './modelPanel';

const training = {
    id: 'c1a2b3c4d5e6f7a8b9c0',
    creation_date: 1793534400, // 2026-11-01 12:00 UTC
    metadata: {
        app: 'yolo',
        experiment: 'baseline',
        run: 'yolo-train-x7k2p',
        kind: 'checkpoints',
        dataset_ref: 'dataset-cars@v3',
        dataset_commit: 'd0d0',
        mlflow_run_id: '0a1b2c',
    },
};
const quantization = {
    id: 'q9q9q9q9q9q9q9q9',
    creation_date: 1793538000,
    metadata: {
        app: 'yolo',
        experiment: 'baseline',
        run: 'yolo-train-x7k2p',
        kind: 'quantization',
        dataset_ref: 'dataset-cars@v3',
    },
};

const inRouter = (ui) => render(<MemoryRouter>{ui}</MemoryRouter>);

beforeEach(() => {
    lakefs.tags = [];
    lakefs.log = [];
    lakefs.commits = {};
    lakefs.fetched = [];
    uiConfig.current = {};
    globalThis.fetch = vi.fn(async (url) => {
        lakefs.url = url;
        return { ok: true, status: 200, json: async () => ({ results: lakefs.log }) };
    });
});

describe('model tags', () => {
    it('parses model-<name>-v<N>', () => {
        expect(parseModelTag('model-yolo-detector-v12')).toEqual({ name: 'yolo-detector', version: 12 });
        expect(parseModelTag('v3')).toBeNull();
        expect(parseModelTag('model-yolo')).toBeNull();
        expect(parseModelTag('model-yolo-v')).toBeNull();
        expect(parseModelTag('model-yolo-v2-rc')).toBeNull();
    });
    it('orders newest first by number within each model and ignores other tags', () => {
        const versions = modelVersions([
            { id: 'model-yolo-v9', commit_id: 'a' },
            { id: 'v1', commit_id: 'b' },
            { id: 'model-yolo-v10', commit_id: 'c' },
            { id: 'model-edge-v1', commit_id: 'd' },
            { id: 'release-1', commit_id: 'e' },
            { id: 'model-yolo-v2', commit_id: 'f' },
        ]);
        expect(versions.map((v) => v.id)).toEqual([
            'model-edge-v1',
            'model-yolo-v10',
            'model-yolo-v9',
            'model-yolo-v2',
        ]);
        expect(versions[1]).toEqual({ id: 'model-yolo-v10', commit_id: 'c', name: 'yolo', version: 10 });
    });
});

describe('runRow', () => {
    it('shows a training commit with its files and MLflow run', () => {
        expect(runRow(training, 'https://mlflow.kaos.eu/')).toEqual({
            id: training.id,
            shortId: 'c1a2b3c4d5e6',
            date: expect.stringMatching(/^2026-11-0[12] \d\d:\d\d$/),
            kind: 'checkpoints',
            experiment: 'baseline',
            run: 'yolo-train-x7k2p',
            datasetRef: 'dataset-cars@v3',
            path: 'checkpoints/baseline/yolo-train-x7k2p/',
            mlflowRun: 'https://mlflow.kaos.eu/#/runs/0a1b2c',
        });
    });
    it('shows dashes for missing metadata', () => {
        expect(runRow({ id: 'abc' }, 'https://mlflow.kaos.eu')).toEqual({
            id: 'abc',
            shortId: 'abc',
            date: DASH,
            kind: DASH,
            experiment: DASH,
            run: DASH,
            datasetRef: '',
            path: '',
            mlflowRun: '',
        });
    });
    it('has no MLflow link without mlflow_url or without a run id', () => {
        expect(runRow(training, '').mlflowRun).toBe('');
        expect(runRow(quantization, 'https://mlflow.kaos.eu').mlflowRun).toBe('');
        expect(runRow(quantization, '').path).toBe('quantization/baseline/yolo-train-x7k2p/');
    });
});

describe('links', () => {
    it('links the console and MLflow only when configured', () => {
        expect(consoleModelsUrl('https://org.console.kaos.eu/')).toBe('https://org.console.kaos.eu/dashboard/models');
        expect(consoleModelsUrl('https://org.console.kaos.eu', 'my proj')).toBe(
            'https://org.console.kaos.eu/dashboard/models?project=my%20proj',
        );
        expect(consoleModelsUrl('')).toBe('');
        expect(mlflowModelsUrl('https://mlflow.kaos.eu')).toBe('https://mlflow.kaos.eu/#/models');
        expect(mlflowModelsUrl(undefined)).toBe('');
        expect(mlflowRunUrl('https://mlflow.kaos.eu', 'r/1')).toBe('https://mlflow.kaos.eu/#/runs/r%2F1');
        expect(mlflowRunUrl('https://mlflow.kaos.eu', '')).toBe('');
    });
    it('names a dataset only for a dataset-* ref', () => {
        expect(datasetOf('dataset-cars@v3')).toEqual({ repo: 'dataset-cars', version: 'v3' });
        expect(datasetOf('e2e-proj@v3')).toBeNull();
        expect(datasetOf('dataset-cars')).toBeNull();
        expect(datasetOf('')).toBeNull();
    });
    it('links a dataset ref to the dataset page in this lakeFS', () => {
        inRouter(<DatasetRef value="dataset-cars@v3" />);
        expect(screen.getByText('dataset-cars@v3').getAttribute('href')).toBe('/datasets/dataset-cars');
    });
    it('shows another ref as text and a missing one as a dash', () => {
        const { container } = inRouter(
            <>
                <DatasetRef value="e2e-proj@main" />
                <DatasetRef value="" />
            </>,
        );
        expect(screen.getByText('e2e-proj@main').tagName).toBe('CODE');
        expect(container.querySelector('a')).toBeNull();
        expect(container.textContent).toContain(DASH);
    });
    it('the header links to the console Models area and to MLflow when set', () => {
        inRouter(
            <ModelHeader app="yolo" consoleUrl="https://org.console.kaos.eu" mlflowUrl="https://mlflow.kaos.eu" />,
        );
        expect(screen.getByText('Model of yolo')).toBeTruthy();
        expect(screen.getByText('Models in the KAOS console').getAttribute('href')).toBe(
            'https://org.console.kaos.eu/dashboard/models',
        );
        expect(screen.getByText('Open in MLflow').getAttribute('href')).toBe('https://mlflow.kaos.eu/#/models');
    });
    it('the header has no links when neither is set', () => {
        inRouter(<ModelHeader app="yolo" orphaned />);
        expect(screen.queryByText('Models in the KAOS console')).toBeNull();
        expect(screen.queryByText('Open in MLflow')).toBeNull();
        expect(screen.getByText(/No app declares this repository any more/)).toBeTruthy();
    });
});

describe('loadModel', () => {
    it('reads model tags and main first-parent, bounded, and only reads tagged commits not in the log', async () => {
        lakefs.tags = [
            { id: 'model-yolo-v1', commit_id: 'old' },
            { id: 'model-yolo-v2', commit_id: training.id },
            { id: 'v1', commit_id: 'x' },
        ];
        lakefs.log = [quantization, { id: 'm1', metadata: {} }, training];
        lakefs.commits = { old: { id: 'old', metadata: { kind: 'checkpoints', experiment: 'first' } } };
        const model = await loadModel('outputs-yolo');
        expect(tags.list).toHaveBeenCalledWith('outputs-yolo', 'model-', '', 100);
        expect(lakefs.url).toBe('/api/v1/repositories/outputs-yolo/refs/main/commits?amount=100&first_parent=true');
        expect(lakefs.fetched).toEqual(['old']);
        expect(model.versions.map((v) => [v.id, v.commit?.id])).toEqual([
            ['model-yolo-v2', training.id],
            ['model-yolo-v1', 'old'],
        ]);
        expect(model.runs.map((c) => c.id)).toEqual([quantization.id, training.id]);
    });
});

describe('ModelPanel', () => {
    it('shows registered versions and training runs with their links', async () => {
        uiConfig.current = { mlflow_url: 'https://mlflow.kaos.eu', console_url: '' };
        lakefs.tags = [{ id: 'model-yolo-v2', commit_id: training.id }];
        lakefs.log = [quantization, training];
        inRouter(<ModelPanel repoId="outputs-yolo" labels={{ 'kaos.app': 'yolo' }} />);
        expect(await screen.findByText('Registered versions')).toBeTruthy();
        expect(screen.getByText('model-yolo-v2')).toBeTruthy();
        expect(screen.getByText('Registered as model-yolo-v2')).toBeTruthy();
        expect(screen.getByText('Open in MLflow')).toBeTruthy();
        expect(screen.queryByText('Models in the KAOS console')).toBeNull();
        const files = screen.getAllByText('Files').map((a) => a.getAttribute('href'));
        expect(files).toContain(
            `/repositories/outputs-yolo/objects?ref=${training.id}&path=checkpoints%2Fbaseline%2Fyolo-train-x7k2p%2F`,
        );
        expect(files).toContain(
            `/repositories/outputs-yolo/objects?ref=${quantization.id}&path=quantization%2Fbaseline%2Fyolo-train-x7k2p%2F`,
        );
        // the training commit in both tables; the quantization commit has no MLflow run
        expect(screen.getAllByText('MLflow run').map((a) => a.getAttribute('href'))).toEqual([
            'https://mlflow.kaos.eu/#/runs/0a1b2c',
            'https://mlflow.kaos.eu/#/runs/0a1b2c',
        ]);
        expect(screen.getByText('c1a2b3c4d5e6').closest('a').getAttribute('href')).toBe(
            `/repositories/outputs-yolo/commits/${training.id}`,
        );
    });
    it('says when no training run has written yet', async () => {
        lakefs.log = [{ id: 'init', metadata: {} }];
        inRouter(<ModelPanel repoId="outputs-yolo" labels={{ 'kaos.app': 'yolo' }} />);
        expect(await screen.findByText('No training run has written to this model yet.')).toBeTruthy();
        expect(screen.queryByText('Registered versions')).toBeNull();
        expect(screen.queryByText('Training runs')).toBeNull();
    });
});
