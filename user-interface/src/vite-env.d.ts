/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_APP_NAME: string;
  readonly VITE_TEAMAGENTICA_KERNEL_URL: string;
  readonly VITE_TEAMAGENTICA_KERNEL_HOST: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
