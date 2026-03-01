/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_ROBOSLOP_KERNEL_HOST: string;
  readonly VITE_ROBOSLOP_KERNEL_PORT: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
