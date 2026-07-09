import { Link } from "react-router-dom";

export function Footer() {
  return (
    <footer>
      <div className="footer-inner">
        <span>&copy; 2026 InstaEdit, Inc.</span>
        <span className="footer-divider" aria-hidden="true">|</span>
        <Link to="/privacy" className="footer-link">
          Privacy Policy
        </Link>
        <span className="footer-divider" aria-hidden="true">|</span>
        <Link to="/terms" className="footer-link">
          Terms of Service
        </Link>
        <span className="footer-divider" aria-hidden="true">|</span>
        <a
          href="https://discord.com/users/1201477873719050332"
          target="_blank"
          rel="noopener noreferrer"
          className="footer-link"
        >
          Discord
        </a>
        <span className="footer-divider" aria-hidden="true">|</span>
        <a
          href="mailto:futurimilionariposta@gmail.com"
          className="footer-link"
        >
          futurimilionariposta@gmail.com
        </a>
      </div>
    </footer>
  );
}
