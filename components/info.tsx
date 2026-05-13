import React from "react";
import styles from "../styles/info.module.scss";

export default function Info() {
  return (
    <div className={styles.info}>
      <p>
        <strong>Wikitrivia Classic</strong> is a fan project keeping the
        original gameplay of Wikitrivia alive.
      </p>
      <p>
        <a
          href="https://github.com/tom-james-watson/wikitrivia"
          target="_blank"
          rel="noopener noreferrer"
        >
          Tom Watson&apos;s original Wikitrivia
        </a>{" "}
        was updated to v2 in 2026 and made many changes. Wikitrivia Classic
        will stay faithful to the 2022 gameplay, with minimal site updates and
        a refreshed dataset. Wikitrivia Classic will never include ads or
        tracking.
      </p>
      <p>
        Original Wikitrivia © 2022 Thomas James Watson, released under the MIT
        license. Card data sourced from{" "}
        <a
          href="https://www.wikidata.org"
          target="_blank"
          rel="noopener noreferrer"
        >
          Wikidata
        </a>{" "}
        under the CC0 license. This fork&apos;s source is MIT licensed and on{" "}
        <a
          href="https://github.com/michaelclarkcuadrado/wikitrivia-classic"
          target="_blank"
          rel="noopener noreferrer"
        >
          GitHub
        </a>
        .
      </p>
      <p>
        Report site issues or reach out by contacting{" "}
        <a href="mailto:wikitrivia@michaelcc.me">wikitrivia@michaelcc.me</a>.
      </p>
    </div>
  );
}
