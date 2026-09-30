import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { AppRootProps, PluginType } from '@grafana/data';
import { LocationServiceProvider, locationService } from '@grafana/runtime';
import { render, screen } from '@testing-library/react';
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

  test('routes the plugin base path to the Overview page', async () => {
    // Mounted the way Grafana mounts an app plugin: under /a/<plugin id>/*.
    // Without that parent route the scenes router matches nothing and the app
    // renders an empty tree, which no assertion on the container can detect.
    render(
      <MemoryRouter
        initialEntries={['/a/tamen25-argus-app/overview']}
        future={{ v7_startTransition: true, v7_relativeSplatPath: true }}
      >
        <LocationServiceProvider service={locationService}>
          <Routes>
            <Route path="/a/tamen25-argus-app/*" element={<App {...props} />} />
          </Routes>
        </LocationServiceProvider>
      </MemoryRouter>
    );
    // The page fetched its data from the plugin backend and rendered it: the
    // route matched, the scene activated, and the content component mounted.
    expect(await screen.findByText(/Fleet Instrumentation Score/)).toBeInTheDocument();
    expect(mockGet).toHaveBeenCalledWith('/api/plugins/tamen25-argus-app/resources/scores');
    expect(screen.getByRole('link', { name: 'Drill into findings' })).toHaveAttribute(
      'href',
      '/a/tamen25-argus-app/scores'
    );
  });
});
