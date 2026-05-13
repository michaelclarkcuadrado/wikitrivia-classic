import React, { useState } from "react";
import { GameState } from "../types/game";
import { Item } from "../types/item";
import createState from "../lib/create-state";
import Board from "./board";
import Loading from "./loading";
import Instructions from "./instructions";
import badCards from "../lib/bad-cards";

export default function Game() {
  const [state, setState] = useState<GameState | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [started, setStarted] = useState(false);
  const [items, setItems] = useState<Item[] | null>(null);

  React.useEffect(() => {
    const fetchGameData = async () => {
      const res = await fetch("/items.json.gz");
      if (!res.ok || !res.body) {
        throw new Error(`Failed to fetch items.json.gz: ${res.status}`);
      }
      const stream = res.body.pipeThrough(new DecompressionStream("gzip"));
      const text = await new Response(stream).text();
      const items: Item[] = text
        .trim()
        .split("\n")
        .map((line) => JSON.parse(line))
        // Filter out questions which give away their answers
        .filter((item) => !item.label.includes(String(item.year)))
        .filter((item) => !item.description.includes(String(item.year)))
        .filter((item) => !/(?:th|st|nd)[ -]century/i.test(item.description))
        .filter((item) => !(item.id in badCards));
      setItems(items);
    };

    fetchGameData();
  }, []);

  React.useEffect(() => {
    (async () => {
      if (items !== null) {
        setState(await createState(items));
        setLoaded(true);
      }
    })();
  }, [items]);

  const resetGame = React.useCallback(() => {
    (async () => {
      if (items !== null) {
        setState(await createState(items));
      }
    })();
  }, [items]);

  const setBoardState = React.useCallback<
    React.Dispatch<React.SetStateAction<GameState>>
  >((update) => {
    setState((prev) => {
      if (prev === null) {
        return prev;
      }
      return typeof update === "function" ? update(prev) : update;
    });
  }, []);

  const [highscore, setHighscore] = React.useState<number>(
    Number(localStorage.getItem("highscore") ?? "0")
  );

  const updateHighscore = React.useCallback((score: number) => {
    localStorage.setItem("highscore", String(score));
    setHighscore(score);
  }, []);

  if (!loaded || state === null) {
    return <Loading />;
  }

  if (!started) {
    return (
      <Instructions highscore={highscore} start={() => setStarted(true)} />
    );
  }

  return (
    <Board
      highscore={highscore}
      state={state}
      setState={setBoardState}
      resetGame={resetGame}
      updateHighscore={updateHighscore}
    />
  );
}
