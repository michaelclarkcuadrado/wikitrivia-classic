import React from "react";
import {
  DragDropContext,
  DropResult,
  SensorAPI,
  useKeyboardSensor,
  useMouseSensor,
} from "@hello-pangea/dnd";
import { GameState } from "../types/game";
import autoMoveSensor from "../lib/autoMoveSensor";
import useTouchSensor from "../lib/touchSensor";
import { checkCorrect, getRandomItem, preloadImage } from "../lib/items";
import NextItemList from "./next-item-list";
import PlayedItemList from "./played-item-list";
import styles from "../styles/board.module.scss";
import Hearts from "./hearts";
import GameOver from "./game-over";

interface Props {
  highscore: number;
  resetGame: () => void;
  state: GameState;
  setState: React.Dispatch<React.SetStateAction<GameState>>;
  updateHighscore: (score: number) => void;
}

export default function Board(props: Props) {
  const { highscore, resetGame, state, setState, updateHighscore } = props;

  const [isDragging, setIsDragging] = React.useState(false);

  const stateRef = React.useRef(state);
  stateRef.current = state;

  const sensors = React.useMemo(
    () => [
      useMouseSensor,
      useKeyboardSensor,
      useTouchSensor,
      (api: SensorAPI) => autoMoveSensor(stateRef.current, api),
    ],
    []
  );

  async function onDragStart() {
    setIsDragging(true);
  }

  async function onDragEnd(result: DropResult) {
    setIsDragging(false);

    const { source, destination } = result;

    if (
      !destination ||
      (source.droppableId === "next" && destination.droppableId === "next")
    ) {
      return;
    }

    if (source.droppableId === "next" && destination.droppableId === "played") {
      setState((prev) => {
        if (prev.next === null) {
          return prev;
        }
        const newDeck = [...prev.deck];
        const newPlayed = [...prev.played];
        const { correct, delta } = checkCorrect(
          newPlayed,
          prev.next,
          destination.index
        );
        newPlayed.splice(destination.index, 0, {
          ...prev.next,
          played: { correct },
        });

        const newNext = prev.nextButOne;
        const newNextButOne = getRandomItem(
          newDeck,
          newNext ? [...newPlayed, newNext] : newPlayed
        );
        const newImageCache = [preloadImage(newNextButOne.image)];

        return {
          ...prev,
          deck: newDeck,
          imageCache: newImageCache,
          next: newNext,
          nextButOne: newNextButOne,
          played: newPlayed,
          lives: correct ? prev.lives : prev.lives - 1,
          badlyPlaced: correct
            ? null
            : {
                index: destination.index,
                rendered: false,
                delta,
              },
        };
      });
    } else if (
      source.droppableId === "played" &&
      destination.droppableId === "played"
    ) {
      setState((prev) => {
        const itemIndex = prev.played.findIndex(
          (p) => p.id === result.draggableId
        );
        if (itemIndex === -1) {
          return { ...prev, badlyPlaced: null };
        }
        const newPlayed = [...prev.played];
        const [moved] = newPlayed.splice(itemIndex, 1);
        newPlayed.splice(destination.index, 0, moved);

        return {
          ...prev,
          played: newPlayed,
          badlyPlaced: null,
        };
      });
    }
  }

  // Ensure that newly placed items are rendered as draggables before trying to
  // move them to the right place if needed.
  React.useLayoutEffect(() => {
    if (
      state.badlyPlaced &&
      state.badlyPlaced.index !== null &&
      !state.badlyPlaced.rendered
    ) {
      setState((prev) =>
        prev.badlyPlaced && !prev.badlyPlaced.rendered
          ? {
              ...prev,
              badlyPlaced: { ...prev.badlyPlaced, rendered: true },
            }
          : prev
      );
    }
  }, [setState, state.badlyPlaced]);

  const score = React.useMemo(() => {
    return state.played.filter((item) => item.played.correct).length - 1;
  }, [state.played]);

  React.useLayoutEffect(() => {
    if (score > highscore) {
      updateHighscore(score);
    }
  }, [score, highscore, updateHighscore]);

  return (
    <DragDropContext
      onDragEnd={onDragEnd}
      onDragStart={onDragStart}
      sensors={sensors}
      enableDefaultSensors={false}
    >
      <div className={styles.wrapper}>
        <div className={styles.top}>
          <Hearts lives={state.lives} />
          {state.lives > 0 ? (
            <>
              <NextItemList
                next={state.next}
                isDraggable={state.badlyPlaced === null}
              />
            </>
          ) : (
            <GameOver
              highscore={highscore}
              resetGame={resetGame}
              score={score}
            />
          )}
        </div>
        <div id="bottom" className={styles.bottom}>
          <PlayedItemList
            badlyPlacedIndex={
              state.badlyPlaced === null ? null : state.badlyPlaced.index
            }
            isDragging={isDragging}
            items={state.played}
          />
        </div>
      </div>
    </DragDropContext>
  );
}
