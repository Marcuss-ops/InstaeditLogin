import { Link } from "react-router-dom";

export function Nav() {
  return (
    <nav>
      <div className="nav-inner">
        <Link to="/" className="logo text-white no-underline">
          InstaEdit
        </Link>
        <Link className="btn btn-primary" to="/login">
          Get started
        </Link>
      </div>
    </nav>
  );
}
