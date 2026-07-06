import { Link } from "react-router-dom";

export function Nav() {
  return (
    <nav>
      <div className="nav-inner">
        <Link to="/" className="logo text-white no-underline">
          InstaEdit
        </Link>
        <div className="hidden md:flex nav-links">
          <a href="#features">Features</a>
          <a href="#how">How it works</a>
          <a href="#pricing">Pricing</a>
          <Link to="/login">Login</Link>
        </div>
        <Link className="btn btn-primary" to="/login">
          Get started
        </Link>
      </div>
    </nav>
  );
}
