import React from "react";
import styles from "../styles/logo.module.scss";

export default function Logo() {
  return (
    <div className={styles.logoWrap}>
      <div className={styles.mark}>
        <span className={styles.main}>Wikitrivia</span>
        <span className={styles.tag}>Classic!</span>
      </div>
    </div>
  );
}
