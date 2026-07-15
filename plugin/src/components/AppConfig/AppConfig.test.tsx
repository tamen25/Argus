import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import AppConfig, { AppConfigProps } from './AppConfig';
import { testIds } from 'components/testIds';

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

  test('renders the engine connection form; save needs a URL', () => {
    // @ts-ignore - `addConfigPage()` and `setChannelSupport()` are not needed here
    render(<AppConfig plugin={props.plugin} query={props.query} />);

    expect(screen.queryByRole('group', { name: /engine connection/i })).toBeInTheDocument();
    const input = screen.getByTestId(testIds.appConfig.engineUrl);
    const save = screen.getByRole('button', { name: /save engine settings/i });
    expect(save).toBeDisabled();

    fireEvent.change(input, { target: { value: 'http://argus-engine.argus.svc:8080' } });
    expect(save).toBeEnabled();
  });

  test('prefills the configured engine URL', () => {
    const plugin = { meta: { ...props.plugin.meta, jsonData: { engineUrl: 'http://elsewhere:8080' } } };
    // @ts-ignore
    render(<AppConfig plugin={plugin} query={props.query} />);
    expect(screen.getByTestId(testIds.appConfig.engineUrl)).toHaveValue('http://elsewhere:8080');
  });
});
