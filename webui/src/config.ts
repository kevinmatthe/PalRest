export type RuntimeConfig = {
  API_BASE_URL?: string;
};

declare global {
  interface Window {
    __PALREST_CONFIG__?: RuntimeConfig;
  }
}

const configuredBase = window.__PALREST_CONFIG__?.API_BASE_URL?.trim() ?? '';

export const apiBaseUrl = configuredBase.replace(/\/+$/, '');
