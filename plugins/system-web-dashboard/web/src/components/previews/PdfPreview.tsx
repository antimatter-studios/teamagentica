import type { PreviewProps } from "./registry";

export default function PdfPreview({ src }: PreviewProps) {
  return (
    <div className="h-full w-full">
      <object data={src} type="application/pdf" className="h-full w-full">
        <div className="flex h-full w-full items-center justify-center p-6 text-sm text-muted-foreground">
          Browser cannot display PDF — download to view
        </div>
      </object>
    </div>
  );
}
