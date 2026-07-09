export function LegalSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="legal-section" style={{ marginBottom: 36 }}>
      <h3
        style={{
          fontSize: "clamp(18px, 2.5vw, 22px)",
          fontWeight: 650,
          color: "white",
          marginBottom: 14,
          letterSpacing: "-0.3px",
        }}
      >
        {title}
      </h3>
      <div style={{ color: "var(--muted)", lineHeight: 1.7, fontSize: 15 }}>
        {children}
      </div>
    </section>
  );
}
