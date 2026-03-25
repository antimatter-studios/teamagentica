import type { PreviewProps } from "./registry";

export default function PdfPreview({ src }: PreviewProps) {
  return (
    <div className="file-preview-pdf-wrap">
      <object
        data={src}
        type="application/pdf"
        className="file-preview-pdf"
      >
        <div className="file-preview-placeholder">
          Browser cannot display PDF — download to view
        </div>
      </object>
    </div>
  );
}
