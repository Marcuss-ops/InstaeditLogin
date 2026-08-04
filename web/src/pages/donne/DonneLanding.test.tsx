import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { DonneLanding } from "./DonneLanding";
import { BookingProvider } from "../../components/booking/BookingProvider";

/**
 * Smoke test per la landing "DonneTube".
 *
 * Obiettivo: un singolo blocco di asserzioni economiche che fallisce
 * appena la promessa centrale dell'hero cambia o la pagina non monta.
 */
describe("DonneLanding", () => {
  it("renders the Italian hero copy", () => {
    render(
      <MemoryRouter>
        <BookingProvider>
          <DonneLanding />
        </BookingProvider>
      </MemoryRouter>,
    );

    const h1 = screen.getByRole("heading", { level: 1 });
    expect(h1).toHaveTextContent(/I Tuoi Primi \$2\.000\/Mese/i);
    expect(h1).toHaveTextContent(/Video Senza Volto/i);
  });

  it("renders the main sections in order", () => {
    render(
      <MemoryRouter>
        <BookingProvider>
          <DonneLanding />
        </BookingProvider>
      </MemoryRouter>,
    );

    const problema = screen.getByRole("heading", {
      name: /Stai perdendo tempo ed energie/i,
    });
    const metodo = screen.getByRole("heading", {
      name: /Semi-automatico\. Massimo guadagno\./i,
    });
    const risultati = screen.getByRole("heading", {
      name: /Entrate reali\./i,
    });
    const faq = screen.getByRole("heading", {
      name: /Domande Frequenti/i,
    });

    expect(problema.compareDocumentPosition(metodo)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(metodo.compareDocumentPosition(risultati)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(risultati.compareDocumentPosition(faq)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });

  it("reuses the six result screenshots from the shared gallery", () => {
    render(
      <MemoryRouter>
        <BookingProvider>
          <DonneLanding />
        </BookingProvider>
      </MemoryRouter>,
    );

    const ids = [
      "result-1.jpg",
      "result-2.jpg",
      "result-3.jpg",
      "result-4.jpg",
      "result-5.jpg",
      "result-6.jpg",
    ];
    for (const id of ids) {
      const img = document.querySelector(`img[src*="${id}"]`);
      expect(img, `missing screenshot ${id}`).not.toBeNull();
    }
  });

  it("renders the video testimonials with the provided shorts", () => {
    render(
      <MemoryRouter>
        <BookingProvider>
          <DonneLanding />
        </BookingProvider>
      </MemoryRouter>,
    );

    const testimonialIds = [
      "AvtS7TToNnc",
      "mLxH7T6dFds",
      "5ohlvIn0GHE",
      "umpasmxyC8U",
      "zM_cMoXFq48",
      "icHG9WxaYsI",
      "1MtkVGYx708",
      "TkotivQzyNw",
      "5m_F5c07tpw",
      "pEk2Ne4FFkQ",
    ];
    for (const id of testimonialIds) {
      const iframe = document.querySelector(
        `iframe[src*="youtube.com/embed/${id}"]`,
      );
      expect(iframe, `missing testimonial short ${id}`).not.toBeNull();
    }
  });
});
