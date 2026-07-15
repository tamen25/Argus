import React, { ChangeEvent, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { css } from '@emotion/css';
import { AppPluginMeta, GrafanaTheme2, PluginConfigPageProps, PluginMeta } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Button, Field, FieldSet, Input, useStyles2 } from '@grafana/ui';
import { testIds } from '../testIds';

type AppPluginSettings = {
  engineUrl?: string;
};

export interface AppConfigProps extends PluginConfigPageProps<AppPluginMeta<AppPluginSettings>> {}

// The only thing Argus needs configured: where the engine lives. The plugin
// backend proxies every browser request through this URL — no credentials,
// the engine API is read-only by design.
const AppConfig = ({ plugin }: AppConfigProps) => {
  const s = useStyles2(getStyles);
  const { enabled, pinned, jsonData } = plugin.meta;
  const [engineUrl, setEngineUrl] = useState(jsonData?.engineUrl ?? '');

  const onChange = (event: ChangeEvent<HTMLInputElement>) => setEngineUrl(event.target.value.trim());

  const onSubmit = (event: React.FormEvent) => {
    event.preventDefault();
    if (!engineUrl) {
      return;
    }
    updatePluginAndReload(plugin.meta.id, { enabled, pinned, jsonData: { engineUrl } });
  };

  return (
    <form onSubmit={onSubmit}>
      <FieldSet label="Engine connection">
        <Field
          label="Engine URL"
          description="Base URL of the argus engine HTTP API. The plugin backend proxies all browser requests through it; the health check pings its /healthz."
        >
          <Input
            width={60}
            name="engineUrl"
            id="config-engine-url"
            data-testid={testIds.appConfig.engineUrl}
            value={engineUrl}
            placeholder="http://argus-engine.argus.svc:8080"
            onChange={onChange}
          />
        </Field>

        <div className={s.marginTop}>
          <Button type="submit" data-testid={testIds.appConfig.submit} disabled={!engineUrl}>
            Save engine settings
          </Button>
        </div>
      </FieldSet>
    </form>
  );
};

export default AppConfig;

const getStyles = (theme: GrafanaTheme2) => ({
  marginTop: css`
    margin-top: ${theme.spacing(3)};
  `,
});

const updatePluginAndReload = async (pluginId: string, data: Partial<PluginMeta<AppPluginSettings>>) => {
  try {
    await updatePlugin(pluginId, data);

    // Reloading the page as the changes made here wouldn't be propagated to the actual plugin otherwise.
    // This is not ideal, however unfortunately currently there is no supported way for updating the plugin state.
    window.location.reload();
  } catch (e) {
    console.error('Error while updating the plugin', e);
  }
};

const updatePlugin = async (pluginId: string, data: Partial<PluginMeta>) => {
  const response = await getBackendSrv().fetch({
    url: `/api/plugins/${pluginId}/settings`,
    method: 'POST',
    data,
  });

  return lastValueFrom(response);
};
