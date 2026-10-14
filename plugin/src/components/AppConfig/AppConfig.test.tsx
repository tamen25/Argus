import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import { of, throwError } from 'rxjs';
import AppConfig, { AppConfigProps } from './AppConfig';
import { testIds } from 'components/testIds';

const fetchMock = jest.fn();
jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ fetch: fetchMock }),
}));

describe('Components/AppConfig', () => {
  let props: AppConfigProps;

  beforeEach(() => {
    jest.resetAllMocks();

    props = {
      plugin: {
        meta: {
          id: 'tamen25-argus-app',
          name: 'Argus',
          type: PluginType.app,
          enabled: true,
          jsonData: {},
        },
      },
      query: {},
    } as unknown as AppConfigProps;
  });

  test('renders the engine connection form; an empty URL saves as the default', () => {
    // @ts-ignore - `addConfigPage()` and `setChannelSupport()` are not needed here
    render(<AppConfig plugin={props.plugin} query={props.query} />);

    expect(screen.queryByRole('group', { name: /engine connection/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /save engine settings/i })).toBeEnabled();
    expect(screen.getByText(/Empty uses the default/)).toBeInTheDocument();
  });

  // R33: a save the backend refuses is shown on the form, not only logged.
  test('shows why the settings were not saved', async () => {
    fetchMock.mockReturnValue(
      throwError(() => ({ status: 400, data: { message: 'engine URL "x": want http(s)://host[:port]' } }))
    );
    // @ts-ignore
    render(<AppConfig plugin={props.plugin} query={props.query} />);
    fireEvent.change(screen.getByTestId(testIds.appConfig.engineUrl), { target: { value: 'x' } });
    fireEvent.click(screen.getByRole('button', { name: /save engine settings/i }));

    expect(await screen.findByText('The settings were not saved')).toBeInTheDocument();
    expect(screen.getByText(/want http\(s\):\/\/host/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /save engine settings/i })).toBeEnabled();
  });

  test('prefills the configured engine URL', () => {
    const plugin = { meta: { ...props.plugin.meta, jsonData: { engineUrl: 'http://elsewhere:8080' } } };
    // @ts-ignore
    render(<AppConfig plugin={plugin} query={props.query} />);
    expect(screen.getByTestId(testIds.appConfig.engineUrl)).toHaveValue('http://elsewhere:8080');
  });

  test('shows a stored API token as configured, without its value', () => {
    const plugin = { meta: { ...props.plugin.meta, secureJsonFields: { apiToken: true } } };
    // @ts-ignore
    render(<AppConfig plugin={plugin} query={props.query} />);
    expect(screen.getByRole('button', { name: /reset/i })).toBeInTheDocument();
  });

  test('saves a new API token as secure data, never as plain settings', async () => {
    fetchMock.mockReturnValue(of({}));
    const reload = jest.fn();
    Object.defineProperty(window, 'location', { value: { reload }, writable: true });
    // @ts-ignore
    render(<AppConfig plugin={props.plugin} query={props.query} />);

    fireEvent.change(screen.getByTestId(testIds.appConfig.engineUrl), { target: { value: 'http://engine:8080' } });
    fireEvent.change(screen.getByTestId(testIds.appConfig.apiToken), { target: { value: 's3cret' } });
    fireEvent.click(screen.getByRole('button', { name: /save engine settings/i }));

    await screen.findByTestId(testIds.appConfig.apiToken);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const { data } = fetchMock.mock.calls[0][0];
    expect(data.jsonData).toEqual({ engineUrl: 'http://engine:8080' });
    expect(data.secureJsonData).toEqual({ apiToken: 's3cret' });
  });
});
