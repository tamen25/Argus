import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { AppRootProps, PluginType } from '@grafana/data';
import { render, waitFor } from '@testing-library/react';
import App from './App';

const mockGet = jest.fn().mockResolvedValue({
  generated_at: '2026-07-14T12:00:00Z',
  argus_version: 'test',
  spec_version: 'test-sha',
  window: '1m0s',
  rule_set_complete: false,
  snapshot: { fleet_score: 100, services: [], rules_evaluated: [] },
});

jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ get: mockGet }),
}));

describe('Components/App', () => {
  let props: AppRootProps;

  beforeEach(() => {
    jest.clearAllMocks();
    props = {
      basename: 'a/tamen25-argus-app',
      meta: {
        id: 'tamen25-argus-app',
        name: 'Argus',
        type: PluginType.app,
        enabled: true,
        jsonData: {},
      },
      query: {},
      path: '',
      onNavChanged: jest.fn(),
    } as unknown as AppRootProps;
  });

  test('mounts the scenes app without throwing', async () => {
    // Route resolution inside SceneApp needs Grafana's router context, which
    // jsdom doesn't have — page behavior is covered by the content-component
    // tests and the Playwright smoke. This guards the scenes wiring itself
    // (e.g. missing browser APIs crashed it before jest-setup stubbed them).
    const { container } = render(
      <MemoryRouter initialEntries={['/a/tamen25-argus-app/overview']}>
        <App {...props} />
      </MemoryRouter>
    );
    await waitFor(() => expect(container).toBeInTheDocument(), { timeout: 2000 });
  });
});
