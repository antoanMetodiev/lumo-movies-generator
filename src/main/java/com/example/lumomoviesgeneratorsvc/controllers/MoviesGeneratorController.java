package com.example.lumomoviesgeneratorsvc.controllers;

import com.example.lumomoviesgeneratorsvc.services.MoviesGeneratorService;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

import java.io.IOException;

@RestController
public class MoviesGeneratorController {

    private final MoviesGeneratorService moviesGeneratorService;

    @Autowired
    public MoviesGeneratorController(MoviesGeneratorService moviesGeneratorService) {
        this.moviesGeneratorService = moviesGeneratorService;
    }

    @PostMapping("/add-movies-by-actor/{imdb_id}")
    public void addMoviesByActor(@PathVariable(name = "imdb_id") String imdb_id) {

        try {
            moviesGeneratorService.addMoviesByActor(imdb_id);
        } catch (Exception e) {
            e.printStackTrace();
        }
    }

    @PostMapping("/search-movies")
    public void searchMovies(@RequestBody String title) throws IOException, InterruptedException {
        System.out.println("====>>> Търся Филми...!!!");
        moviesGeneratorService.searchForMovies(title);
    }
}