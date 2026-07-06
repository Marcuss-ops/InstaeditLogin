export function Steps() {
  const items = [
    {
      num: 1,
      title: "Upload assets",
      desc: "Submit your raw footage, ideas, and style guidelines to our portal in seconds.",
    },
    {
      num: 2,
      title: "Professional editing",
      desc: "Our specialized video editors cut, color-grade, animate, and sound-design your videos.",
    },
    {
      num: 3,
      title: "Review & publish",
      desc: "Review the draft, request unlimited revisions, then export directly to your socials.",
    },
  ];

  return (
    <section className="section" id="how">
      <div className="section-head">
        <h2>How it works</h2>
        <p>Three simple steps. From upload to publishing.</p>
      </div>
      <div className="steps">
        {items.map((item) => (
          <div key={item.num} className="step">
            <div className="step-num">{item.num}</div>
            <h3 className="text-white font-bold">{item.title}</h3>
            <p>{item.desc}</p>
          </div>
        ))}
      </div>
    </section>
  );
}
