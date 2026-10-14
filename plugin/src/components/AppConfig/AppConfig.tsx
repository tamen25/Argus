import React, { ChangeEvent, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { css } from '@emotion/css';
import { AppPluginMeta, GrafanaTheme2, PluginConfigPageProps, PluginMeta } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Field, FieldSet, Input, SecretInput, useStyles2 } from '@grafana/ui';
import { errorText } from '../../api/useEngineData';
import { testIds } from '../testIds';

type AppPluginSettings = {
  engineUrl?: string;
};

// The engine's API token is a secure setting (secureJsonData.apiToken):
// Grafana stores it encrypted and only the plugin backend ever reads it.
type AppSecureSettings = {
  apiToken?: string;
};

export interface AppConfigProps extends PluginConfigPageProps<AppPluginMeta<AppPluginSettings>> {}

// Where the engine lives, and the token its API requires. The plugin backend
// proxies every browser request through this URL, adding the token.
const AppConfig = ({ plugin }: AppConfigProps) => {
  const s = useStyles2(getStyles);
  const { enabled, pinned, jsonData, secureJsonFields } = plugin.meta;
  const [engineUrl, setEngineUrl] = useState(jsonData?.engineUrl ?? '');
  // Grafana never sends a stored secret back, only whether one is set.
  const [apiToken, setApiToken] = useState('');
  const [isApiTokenSet, setIsApiTokenSet] = useState(Boolean(secureJsonFields?.apiToken));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const onSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setSaveError(null);
    // A stored token is left alone unless it was reset; a reset one is
    // replaced by what was typed (empty clears it).
    const secureJsonData: AppSecureSettings | undefined = isApiTokenSet ? undefined : { apiToken };
    try {
      // An empty URL is saved as empty: the backend then uses the default.
      await updatePlugin(plugin.meta.id, { enabled, pinned, jsonData: { engineUrl }, secureJsonData });
    } catch (e) {
      // The backend refuses a URL it cannot use, and says why.
      setSaveError(errorText(e));
      setSaving(false);
      return;
    }
    // Settings reach the running plugin only on a reload; there is no
    // supported way to update its state in place.
    window.location.reload();
  };

  return (
    <form onSubmit={onSubmit}>
      <FieldSet label="Engine connection">
        <Field
          label="Engine URL"
          description="Base URL of the argus engine HTTP API, without credentials, query or fragment. The plugin backend proxies all browser requests through it; the health check pings its /healthz. Empty uses the default, http://argus-engine.argus.svc:8080."
        >
          <Input
            width={60}
            name="engineUrl"
            id="config-engine-url"
            data-testid={testIds.appConfig.engineUrl}
            value={engineUrl}
            placeholder="http://argus-engine.argus.svc:8080"
            onChange={(event: ChangeEvent<HTMLInputElement>) => setEngineUrl(event.target.value.trim())}
          />
        </Field>

        <Field
          label="API token"
          description="The engine's ARGUS_API_TOKEN. The Helm chart generates one; the release notes (helm status) show how to read it. Stored encrypted, never sent to browsers."
        >
          <SecretInput
            width={60}
            id="config-api-token"
            data-testid={testIds.appConfig.apiToken}
            value={apiToken}
            isConfigured={isApiTokenSet}
            placeholder="Leave empty if the engine runs without a token"
            onChange={(event: ChangeEvent<HTMLInputElement>) => setApiToken(event.target.value.trim())}
            onReset={() => {
              setApiToken('');
              setIsApiTokenSet(false);
            }}
          />
        </Field>

        {saveError && (
          <Alert title="The settings were not saved" severity="error" className={s.marginTop}>
            {saveError}
          </Alert>
        )}
        <div className={s.marginTop}>
          <Button type="submit" data-testid={testIds.appConfig.submit} disabled={saving}>
            {saving ? 'Saving…' : 'Save engine settings'}
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

type PluginSettingsUpdate = Partial<PluginMeta<AppPluginSettings>> & { secureJsonData?: AppSecureSettings };

const updatePlugin = async (pluginId: string, data: PluginSettingsUpdate) => {
  const response = await getBackendSrv().fetch({
    url: `/api/plugins/${pluginId}/settings`,
    method: 'POST',
    data,
  });

  return lastValueFrom(response);
};
