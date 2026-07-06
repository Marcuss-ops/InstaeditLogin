import { Link } from "react-router-dom";

export function CTA() {
  return (
    <section className="cta">
      <h2>Ready to create premium videos?</h2>
      <Link className="btn btn-primary" to="/login">
        Get started today
      </Link>
    </section>
  );
}
